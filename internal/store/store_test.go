package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // for the admin connection that creates test schemas

	"github.com/chrthal/accountant/internal/store"
)

// dsns returns every engine this run can actually test against. SQLite always
// runs; Postgres runs when TEST_POSTGRES_DSN points at a throwaway database
// (CI sets it from a service container — see .github/workflows/ci.yaml).
//
// Each caller gets an empty database on both engines: SQLite via a temp file,
// Postgres via a schema of its own. Sharing one Postgres database across tests
// makes "nothing is configured yet" assertions depend on execution order.
func dsns(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{"sqlite": "sqlite://" + filepath.Join(t.TempDir(), "test.db")}
	if base := os.Getenv("TEST_POSTGRES_DSN"); base != "" {
		out["postgres"] = freshSchema(t, base)
	}
	return out
}

var schemaCounter atomic.Int64

// freshSchema creates a private schema on the test database and returns a DSN
// scoped to it, dropping it when the test ends.
func freshSchema(t *testing.T, base string) string {
	t.Helper()

	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("connect to the test database: %v", err)
	}
	defer admin.Close()

	name := fmt.Sprintf("t%d_%s", schemaCounter.Add(1), nonAlnum.ReplaceAllString(strings.ToLower(t.Name()), "_"))
	if len(name) > 60 {
		name = name[:60]
	}
	if _, err := admin.Exec("CREATE SCHEMA " + name); err != nil {
		t.Fatalf("create schema %s: %v", name, err)
	}
	t.Cleanup(func() {
		db, err := sql.Open("pgx", base)
		if err != nil {
			return
		}
		defer db.Close()
		db.Exec("DROP SCHEMA " + name + " CASCADE")
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse TEST_POSTGRES_DSN: %v", err)
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	return u.String()
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func TestOpenAndHealth(t *testing.T) {
	for engine, dsn := range dsns(t) {
		t.Run(engine, func(t *testing.T) {
			st, err := store.Open(dsn)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer st.Close()

			if err := st.Health(context.Background()); err != nil {
				t.Errorf("health: %v", err)
			}
			if string(st.Dialect) != engine {
				t.Errorf("dialect = %q, want %q", st.Dialect, engine)
			}
		})
	}
}

// Migrations run on every Open, so opening the same database twice must be a
// no-op the second time rather than a duplicate-apply error.
func TestMigrationsAreIdempotent(t *testing.T) {
	for engine, dsn := range dsns(t) {
		t.Run(engine, func(t *testing.T) {
			for i := range 2 {
				st, err := store.Open(dsn)
				if err != nil {
					t.Fatalf("open %d: %v", i+1, err)
				}
				st.Close()
			}

			st, err := store.Open(dsn)
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			defer st.Close()

			var n int
			q := st.Rebind("SELECT COUNT(*) FROM schema_migrations WHERE name = ?")
			if err := st.DB.QueryRow(q, "0001_init.sql").Scan(&n); err != nil {
				t.Fatalf("count migrations: %v", err)
			}
			if n != 1 {
				t.Errorf("0001_init.sql applied %d times, want 1", n)
			}
		})
	}
}

// The point of Rebind is that one query string works on both engines.
func TestRebindRoundTrip(t *testing.T) {
	for engine, dsn := range dsns(t) {
		t.Run(engine, func(t *testing.T) {
			st, err := store.Open(dsn)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer st.Close()

			var value string
			q := st.Rebind(`SELECT value FROM app_meta WHERE "key" = ?`)
			if err := st.DB.QueryRow(q, "schema_origin").Scan(&value); err != nil {
				t.Fatalf("query: %v", err)
			}
			if value != "phase-0" {
				t.Errorf("value = %q, want %q", value, "phase-0")
			}
		})
	}
}

func TestUnsupportedDSN(t *testing.T) {
	for _, dsn := range []string{"mysql://root@localhost/x", ""} {
		if _, err := store.Open(dsn); err == nil {
			t.Errorf("Open(%q) succeeded, want an error", dsn)
		}
	}
}
