package store

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// A backup made before migration 0007 still has the bank match in
// bank_transactions.matched_entry_id. Restoring it with today's binary must
// load the rows at their own schema and then run 0007, which moves the match
// into bank_matches, exactly as it did for live databases.
func TestRestoreOfAnOlderSchemaRunsTheLaterMigrations(t *testing.T) {
	ctx := context.Background()
	const at = "0006_invoice_payments.sql"

	old, err := openMigrated("sqlite://"+filepath.Join(t.TempDir(), "old.db"), at)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, q := range []string{
		`INSERT INTO entries (id, booked_on, direction, description, amount_gross_cents, vat_rate,
			vat_cents, net_cents, created_at) VALUES (1, '2026-03-15', 'outgoing', 'Miete', 12000, 20, 2000, 10000, ?)`,
		`INSERT INTO bank_transactions (id, booked_on, amount_cents, row_hash, imported_at, matched_entry_id)
			VALUES (1, '2026-03-15', -12000, 'h1', ?, 1)`,
	} {
		if _, err := old.DB.Exec(q, now); err != nil {
			t.Fatal(err)
		}
	}

	// Write the backup by hand with the tables and columns of that era, taken
	// from the old database itself, so later migrations' columns stay out.
	live, err := old.liveSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var file bytes.Buffer
	gz := gzip.NewWriter(&file)
	w := bufio.NewWriter(gz)
	line := func(v any) { b, _ := json.Marshal(v); w.Write(append(b, '\n')) }
	migrations, _ := old.appliedMigrations(ctx, old.DB)
	line(backupHeader{Format: BackupFormat, Version: backupVersion, Engine: SQLite, CreatedAt: now, Migrations: migrations})
	tables := 0
	for _, tb := range backupTables {
		if live[tb.Name] == nil {
			continue // created by a later migration
		}
		var cols []backupColumn
		for _, c := range tb.Columns {
			if live[tb.Name][c.Name] {
				cols = append(cols, c)
			}
		}
		line(tableStart{Table: tb.Name, Columns: cols})
		n, hash, err := old.dumpTable(ctx, old.DB, tb.Name, tb.OrderBy, cols, func(l []byte) error {
			_, err := w.Write(l)
			return err
		})
		if err != nil {
			t.Fatalf("dump %s: %v", tb.Name, err)
		}
		line(tableEnd{End: tb.Name, Rows: n, SHA256: hash})
		tables++
	}
	line(backupTrailer{Complete: true, Tables: tables})
	w.Flush()
	gz.Close()
	old.Close()

	dst := "sqlite://" + filepath.Join(t.TempDir(), "new.db")
	if _, err := Restore(ctx, dst, bytes.NewReader(file.Bytes())); err != nil {
		t.Fatalf("restore an older backup: %v", err)
	}
	s, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tx, err := s.GetBankTransaction(ctx, 1)
	if err != nil || len(tx.Matches) != 1 || tx.Matches[0].ID != 1 {
		t.Errorf("after restore the bank match = %+v (%v), want it moved into bank_matches by 0007", tx.Matches, err)
	}
	if err := s.CheckBackupCoverage(ctx); err != nil {
		t.Errorf("the restored database is not at the current schema: %v", err)
	}
}
