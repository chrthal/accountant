package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestSQLiteAccountsRebuildOnExistingData covers the risky half of migration
// 0005: SQLite cannot drop a CHECK constraint, so it rebuilds `entries` — a
// table with append-only triggers on it and rows pointing at it from two
// directions.
//
// A fresh database passes that trivially, which is why this one is not fresh.
// It builds a database at the pre-0005 schema, seeds a booking, its Storno and
// a matched bank transaction, and then runs the real migrator over it.
func TestSQLiteAccountsRebuildOnExistingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.db")
	seedPre0005(t, path)

	s, err := Open("sqlite://" + path)
	if err != nil {
		t.Fatalf("open and migrate an existing database: %v", err)
	}
	defer s.Close()

	// Data survived, ids intact.
	var n int
	mustScan(t, s.DB, `SELECT COUNT(*) FROM entries`, &n)
	if n != 2 {
		t.Fatalf("entries after rebuild = %d, want 2", n)
	}
	var konto, desc string
	mustScan(t, s.DB, `SELECT konto, description FROM entries WHERE id = 1`, &konto, &desc)
	if konto != "7600" || desc != "Büromaterial" {
		t.Fatalf("entry 1 after rebuild = %q/%q, want \"7600\"/\"Büromaterial\"", konto, desc)
	}

	// References still resolve, in both directions.
	var reverses, matched int64
	mustScan(t, s.DB, `SELECT reverses_entry_id FROM entries WHERE id = 2`, &reverses)
	// 0007 moved the match into bank_matches (ADR 0014), so that is where the
	// pre-0005 link has to arrive.
	mustScan(t, s.DB, `SELECT entry_id FROM bank_matches WHERE bank_transaction_id = 1`, &matched)
	if reverses != 1 || matched != 1 {
		t.Fatalf("references after rebuild: reverses=%d matched=%d, want 1/1", reverses, matched)
	}

	// Foreign keys are enforced again once the migrator is done — it turns them
	// off only for the migration itself.
	var fk int
	mustScan(t, s.DB, `PRAGMA foreign_keys`, &fk)
	if fk != 1 {
		t.Fatal("foreign_keys left off after migrating")
	}

	// The append-only triggers came back with the table.
	if _, err := s.DB.Exec(`UPDATE entries SET description = 'x' WHERE id = 1`); err == nil {
		t.Fatal("entries are updatable after the rebuild — the trigger was lost")
	}
	if _, err := s.DB.Exec(`DELETE FROM entries WHERE id = 1`); err == nil {
		t.Fatal("entries are deletable after the rebuild — the trigger was lost")
	}

	// A blank Konto is accepted now, and the AUTOINCREMENT sequence carried
	// over rather than restarting at 1 and colliding.
	id, err := s.CreateEntry(context.Background(), NewEntry{
		BookedOn: "2026-04-01", Direction: "outgoing", Description: "ohne Konto",
		AmountGrossCents: 5000, VATRate: 20,
	})
	if err != nil {
		t.Fatalf("booking without accounts should be allowed after 0005: %v", err)
	}
	if id != 3 {
		t.Fatalf("new entry id = %d, want 3 — the id sequence restarted", id)
	}
}

// seedPre0005 builds a database at the 0004 schema with data in it, recording
// those migrations as applied so the real migrator picks up at 0005.
func seedPre0005(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteConnStr(path))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	mustExec(t, db, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY, applied_at TIMESTAMP NOT NULL)`)
	for _, m := range []string{"0001_init.sql", "0002_core.sql", "0003_invoices.sql", "0004_bank.sql"} {
		stmts, err := migrationFS.ReadFile("migrations/sqlite/" + m)
		if err != nil {
			t.Fatalf("read %s: %v", m, err)
		}
		if _, err := db.Exec(string(stmts)); err != nil {
			t.Fatalf("apply %s: %v", m, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`,
			m, time.Now().UTC()); err != nil {
			t.Fatalf("record %s: %v", m, err)
		}
	}

	// A booking, the Storno that reverses it, and a bank transaction matched to
	// the original — every reference into `entries` the schema allows.
	mustExec(t, db, `INSERT INTO entries
		(id, booked_on, direction, description, amount_gross_cents, vat_rate,
		 vat_cents, net_cents, konto, gegenkonto, created_at)
		VALUES (1, '2026-03-01', 'outgoing', 'Büromaterial', 12000, 20, 2000, 10000,
		        '7600', '2700', '2026-03-01T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO entries
		(id, booked_on, direction, description, amount_gross_cents, vat_rate,
		 vat_cents, net_cents, konto, gegenkonto, reverses_entry_id, created_at)
		VALUES (2, '2026-03-02', 'income', 'Storno zu #1', 12000, 20, 2000, 10000,
		        '2700', '7600', 1, '2026-03-02T00:00:00Z')`)
	mustExec(t, db, `INSERT INTO bank_transactions
		(id, booked_on, amount_cents, row_hash, imported_at, matched_entry_id)
		VALUES (1, '2026-03-01', -12000, 'hash-1', '2026-03-01T00:00:00Z', 1)`)

	// The constraint 0005 exists to remove — if this ever stops failing, the
	// migration is testing nothing.
	_, err = db.Exec(`INSERT INTO entries
		(booked_on, direction, description, amount_gross_cents, vat_rate,
		 vat_cents, net_cents, konto, gegenkonto, created_at)
		VALUES ('2026-04-01', 'outgoing', 'x', 5000, 20, 833, 4167, '', '', '2026-04-01T00:00:00Z')`)
	if err == nil {
		t.Fatal("the pre-0005 schema accepted an empty konto — nothing to migrate")
	}
}

func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("exec %s: %v", q, err)
	}
}

func mustScan(t *testing.T, db *sql.DB, q string, dest ...any) {
	t.Helper()
	if err := db.QueryRow(q).Scan(dest...); err != nil {
		t.Fatalf("query %s: %v", q, err)
	}
}
