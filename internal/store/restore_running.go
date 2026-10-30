package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// dataMigrations change or move existing rows, not only the schema. A backup
// from before one of them must go through it, which only the command-line
// restore does: it migrates the target step by step. Every other migration
// only adds tables and columns with defaults, so rows from an older backup
// fit the current schema as they are. TestDataMigrationsAreListed fails when
// a migration that writes rows is missing here.
var dataMigrations = []string{"0005_accounts.sql", "0007_bank_matches.sql"}

// ErrNotFresh is returned by RestoreRunning when the database holds books.
var ErrNotFresh = errors.New("this installation already holds data")

// ErrNeedsCLI is returned by RestoreRunning for a backup that only the
// command-line restore can bring up to date.
var ErrNeedsCLI = errors.New("this backup is from before a data migration")

// Fresh reports whether the database is a new installation: nothing in it
// but, at most, the untouched default categories (ADR 0027).
func (s *Store) Fresh(ctx context.Context) (bool, error) {
	err := s.requireFresh(ctx, s.DB)
	if errors.Is(err, ErrNotFresh) {
		return false, nil
	}
	return err == nil, err
}

// RestoreRunning loads a backup into the database this store is open on,
// for the restore in the browser (ADR 0030). The database must be fresh; the
// default categories, if that is all it holds, are replaced in the same
// transaction. The backup is read twice: once to check it completely, then
// to load it, so a damaged file changes nothing. open returns the backup
// from its start each time.
func (s *Store) RestoreRunning(ctx context.Context, open func() (io.ReadCloser, error)) (BackupSummary, error) {
	f, err := open()
	if err != nil {
		return BackupSummary{}, err
	}
	sum, err := VerifyBackup(f)
	f.Close()
	if err != nil {
		return BackupSummary{}, err
	}

	known, err := s.embeddedMigrations()
	if err != nil {
		return BackupSummary{}, err
	}
	if len(sum.Migrations) == 0 || len(sum.Migrations) > len(known) || !slices.Equal(known[:len(sum.Migrations)], sum.Migrations) {
		return BackupSummary{}, fmt.Errorf("the backup was made by a newer or different version of the app")
	}
	for _, m := range known[len(sum.Migrations):] {
		if slices.Contains(dataMigrations, m) {
			return BackupSummary{}, fmt.Errorf("%w (%s)", ErrNeedsCLI, strings.TrimSuffix(m, ".sql"))
		}
	}

	f, err = open()
	if err != nil {
		return BackupSummary{}, err
	}
	defer f.Close()
	br, closeFn, err := newBackupReader(f)
	if err != nil {
		return BackupSummary{}, err
	}
	defer closeFn()
	h, err := br.header()
	if err != nil {
		return BackupSummary{}, err
	}
	return s.loadBackup(ctx, br, h, func(tx *sql.Tx) error {
		// Checked again inside the transaction: nothing may have been
		// booked between the page load and the upload.
		if err := s.requireFresh(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM categories`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, s.Rebind(`DELETE FROM settings WHERE "key" = ?`), "last_backup_at")
		return err
	})
}

// requireFresh fails with ErrNotFresh unless every table that backups cover
// is empty, apart from categories that are exactly the defaults and the
// time of the last backup: the daily backup runs at the first start, before
// anyone could restore.
func (s *Store) requireFresh(ctx context.Context, q interface {
	queryer
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}) error {
	for _, t := range backupTables {
		if t.Name == "categories" {
			continue
		}
		count := "SELECT COUNT(*) FROM " + quoteIdent(t.Name)
		if t.Name == "settings" {
			count += ` WHERE "key" <> 'last_backup_at'`
		}
		var n int
		if err := q.QueryRowContext(ctx, count).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: table %s has %d rows", ErrNotFresh, t.Name, n)
		}
	}
	rows, err := q.QueryContext(ctx, `SELECT name, direction, keywords, default_vat_rate FROM categories`)
	if err != nil {
		return err
	}
	defer rows.Close()
	defaults := map[string]Category{}
	for _, c := range DefaultCategories {
		defaults[c.Name] = c
	}
	for rows.Next() {
		var (
			c   Category
			vat sql.NullInt64
		)
		if err := rows.Scan(&c.Name, &c.Direction, &c.Keywords, &vat); err != nil {
			return err
		}
		d, ok := defaults[c.Name]
		same := ok && d.Direction == c.Direction && normaliseKeywords(d.Keywords) == c.Keywords &&
			(d.DefaultVATRate == nil) == !vat.Valid && (d.DefaultVATRate == nil || int64(*d.DefaultVATRate) == vat.Int64)
		if !same {
			return fmt.Errorf("%w: category %q is not one of the defaults", ErrNotFresh, c.Name)
		}
	}
	return rows.Err()
}
