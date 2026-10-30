// Package store owns the database: opening it, keeping the schema current, and
// handing out the *sql.DB the rest of the app queries through.
//
// Two engines are supported (ADR 0006): PostgreSQL, which is what a real
// deployment runs, and SQLite, which keeps `make dev` and the test suite to a
// single binary with no server to start. Queries elsewhere in the app must stay
// in the dialect-portable subset — see the Rebind helper for placeholders and
// migrations/ for the per-dialect schema.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for PostgreSQL
	_ "modernc.org/sqlite"             // pure-Go SQLite driver, no cgo (ADR 0001)
)

//go:embed migrations/sqlite/*.sql migrations/postgres/*.sql
var migrationFS embed.FS

// Dialect identifies which SQL engine a Store is talking to.
type Dialect string

const (
	SQLite   Dialect = "sqlite"
	Postgres Dialect = "postgres"
)

// DefaultDSN is used when neither the -dsn flag nor BOOKKEEPER_DSN is set:
// a local SQLite file, so a fresh checkout runs with no setup.
const DefaultDSN = "sqlite://bookkeeper.db"

// Store wraps the database handle and remembers which dialect it speaks.
type Store struct {
	DB      *sql.DB
	Dialect Dialect

	// upTo stops migrations after the named one; empty means all. Only a
	// restore sets it, to load an older backup at its own schema (ADR 0019).
	upTo string
}

// DSNFromEnv returns the DSN configured via BOOKKEEPER_DSN, or DefaultDSN.
func DSNFromEnv() string {
	if dsn := os.Getenv("BOOKKEEPER_DSN"); dsn != "" {
		return dsn
	}
	return DefaultDSN
}

// Open connects to the database named by dsn and applies any migrations it
// hasn't seen yet. The scheme selects the engine:
//
//	postgres://user:pass@host:5432/bookkeeper?sslmode=disable
//	sqlite://bookkeeper.db      (or a bare path: ./bookkeeper.db, /data/x.db)
func Open(dsn string) (*Store, error) { return openMigrated(dsn, "") }

func openMigrated(dsn, upTo string) (*Store, error) {
	dialect, driver, connStr, err := parseDSN(dsn)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open(driver, connStr)
	if err != nil {
		return nil, err
	}

	if dialect == SQLite {
		// SQLite permits exactly one writer; more pooled connections buy
		// nothing and turn contention into "database is locked".
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(5)
		db.SetConnMaxLifetime(time.Hour)
	}

	// Postgres may still be starting up when the app container does; a short
	// retry beats crash-looping the container on a cold `compose up`.
	if err := ping(db, dialect); err != nil {
		db.Close()
		return nil, err
	}

	s := &Store{DB: db, Dialect: dialect, upTo: upTo}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func ping(db *sql.DB, dialect Dialect) error {
	attempts := 1
	if dialect == Postgres {
		attempts = 10
	}
	var err error
	for i := range attempts {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err = db.PingContext(ctx)
		cancel()
		if err == nil {
			return nil
		}
		if i < attempts-1 {
			time.Sleep(time.Second)
		}
	}
	return fmt.Errorf("connect: %w", err)
}

func parseDSN(dsn string) (dialect Dialect, driver, connStr string, err error) {
	switch {
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		connStr, err := postgresConnStr(dsn)
		return Postgres, "pgx", connStr, err

	case strings.HasPrefix(dsn, "sqlite://"), strings.HasPrefix(dsn, "file:"):
		path := strings.TrimPrefix(strings.TrimPrefix(dsn, "sqlite://"), "file:")
		return SQLite, "sqlite", sqliteConnStr(path), nil

	case dsn == "":
		return "", "", "", fmt.Errorf("empty DSN")

	case strings.Contains(dsn, "://"):
		scheme, _, _ := strings.Cut(dsn, "://")
		return "", "", "", fmt.Errorf("unsupported database %q — use postgres:// or sqlite://", scheme)

	default:
		// A bare path is a SQLite file.
		return SQLite, "sqlite", sqliteConnStr(dsn), nil
	}
}

// postgresConnStr forces pgx's simple query protocol unless the DSN already
// picks a mode. pgx's default extended protocol rejects multi-statement
// strings, which is exactly what a migration file is; simple protocol also
// keeps things working behind a transaction-pooling pgbouncer. Arguments are
// still escaped by pgx, so this is not a SQL-injection trade.
func postgresConnStr(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse postgres DSN: %w", err)
	}
	q := u.Query()
	if q.Get("default_query_exec_mode") == "" {
		q.Set("default_query_exec_mode", "simple_protocol")
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func sqliteConnStr(path string) string {
	if strings.Contains(path, "?") {
		return path
	}
	// WAL plus a busy timeout: a short wait beats an immediate
	// "database is locked" for our level of concurrency.
	return path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
}

// Close releases the database handle.
func (s *Store) Close() error { return s.DB.Close() }

// Health verifies the database is actually reachable — this backs /healthz, so
// it must hit the database rather than report a constant.
func (s *Store) Health(ctx context.Context) error {
	var one int
	return s.DB.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// Rebind converts a query written with `?` placeholders into the dialect's own
// form ($1, $2, … on Postgres). Every query in the app is written with `?` and
// passed through here, so a single query string works on both engines.
func (s *Store) Rebind(query string) string {
	if s.Dialect != Postgres {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// migrate applies the embedded migrations for this dialect in filename order,
// recording each in schema_migrations so reruns are no-ops. Deliberately no
// migration library: ordered files plus one bookkeeping table is the whole
// requirement.
//
// Everything runs on one pinned connection. That matters for SQLite, where
// migrateSQLite below turns foreign-key enforcement off for the duration — a
// connection-scoped pragma, so it has to be the same connection the migrations
// run on, not whichever one the pool hands out next.
func (s *Store) migrate() error {
	ctx := context.Background()
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if s.Dialect == SQLite {
		return s.migrateSQLite(ctx, conn)
	}
	return s.runMigrations(ctx, conn)
}

// migrateSQLite runs the migrations with foreign keys disabled, then verifies
// none were broken.
//
// SQLite cannot drop or alter a constraint, so a schema change like 0005 has to
// rebuild the table: create, copy, drop, rename. With enforcement on, dropping
// a table that other rows reference fails, and `defer_foreign_keys` does not
// help — its violation counter is incremented by the drop and never cleared by
// the table that replaces it. Disabling enforcement around the migration is
// step 1 of SQLite's own documented procedure for exactly this
// (sqlite.org/lang_altertable.html#otheralter).
//
// `foreign_keys` is a no-op inside a transaction, so it is set here, outside
// the per-file transactions, and the foreign_key_check afterwards is what makes
// turning it off safe rather than merely convenient: a migration that orphans a
// row fails the startup that applied it, not some later query.
func (s *Store) migrateSQLite(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	migrateErr := s.runMigrations(ctx, conn)
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil && migrateErr == nil {
		return err
	}
	if migrateErr != nil {
		return migrateErr
	}

	rows, err := conn.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	var broken []string
	for rows.Next() {
		var table, parent sql.NullString
		var rowid, fkid sql.NullInt64
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return err
		}
		broken = append(broken, fmt.Sprintf("%s row %d -> %s", table.String, rowid.Int64, parent.String))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(broken) > 0 {
		return fmt.Errorf("migrations left broken foreign keys: %s", strings.Join(broken, "; "))
	}
	return nil
}

func (s *Store) runMigrations(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name       TEXT PRIMARY KEY,
		applied_at TIMESTAMP NOT NULL
	)`); err != nil {
		return err
	}

	dir := "migrations/" + string(s.Dialect)
	names, err := s.embeddedMigrations()
	if err != nil {
		return err
	}

	countQ := s.Rebind("SELECT COUNT(*) FROM schema_migrations WHERE name = ?")
	insertQ := s.Rebind("INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)")

	for _, name := range names {
		if s.upTo != "" && name > s.upTo {
			break
		}
		var applied int
		if err := conn.QueryRowContext(ctx, countQ, name).Scan(&applied); err != nil {
			return err
		}
		if applied > 0 {
			continue
		}

		stmts, err := migrationFS.ReadFile(dir + "/" + name)
		if err != nil {
			return err
		}

		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		// Both engines run DDL inside a transaction, so a failed migration
		// leaves no half-applied schema behind.
		if _, err := tx.ExecContext(ctx, string(stmts)); err != nil {
			tx.Rollback()
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, insertQ, name, time.Now().UTC()); err != nil {
			tx.Rollback()
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// embeddedMigrations lists this binary's migrations for the dialect, in the
// order they apply.
func (s *Store) embeddedMigrations() ([]string, error) {
	entries, err := migrationFS.ReadDir("migrations/" + string(s.Dialect))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}
