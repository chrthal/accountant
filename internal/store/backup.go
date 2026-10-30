package store

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Backups are a logical export of every table into one gzipped JSON-lines
// file (ADR 0019). One format serves both engines, so a SQLite backup restores
// into Postgres and the other way round, and each table carries a checksum so
// a restore can prove it is complete.
//
// File layout, one JSON value per line:
//
//	{"format":"kmu-buchhaltung-backup","version":1,...,"migrations":[...]}
//	{"table":"contacts","columns":[{"name":"id","kind":"int"},...]}
//	[1,"customer","Muster GmbH",...]          one line per row
//	{"end":"contacts","rows":1,"sha256":"…"}  hash over the row lines
//	...
//	{"complete":true,"tables":9}              a file without it is truncated

// BackupFormat names the file format in its header line.
const BackupFormat = "kmu-buchhaltung-backup"

const backupVersion = 1

type colKind string

const (
	kindInt   colKind = "int"
	kindText  colKind = "text"
	kindTime  colKind = "time"
	kindBytes colKind = "bytes"
)

type backupColumn struct {
	Name string  `json:"name"`
	Kind colKind `json:"kind"`
}

type backupTable struct {
	Name    string
	OrderBy string
	Columns []backupColumn
	HasID   bool // has an identity id whose sequence must follow a restore
}

func cols(spec ...string) []backupColumn {
	out := make([]backupColumn, 0, len(spec))
	for _, s := range spec {
		name, kind, _ := strings.Cut(s, ":")
		out = append(out, backupColumn{Name: name, Kind: colKind(kind)})
	}
	return out
}

// backupTables is the current schema, in foreign-key order: a row is only
// written after every row it references. TestBackupCoversTheWholeSchema fails
// when a migration adds a table or a column that is not listed here, so a new
// table cannot silently go missing from backups.
//
// app_meta and schema_migrations are left out on purpose. Both belong to the
// migrator, and the restore target gets its own from its own migrations.
var backupTables = []backupTable{
	{Name: "settings", OrderBy: `"key"`, Columns: cols("key:text", "value:text")},
	{Name: "contacts", OrderBy: "id", HasID: true, Columns: cols(
		"id:int", "kind:text", "name:text", "uid:text", "address:text", "email:text", "created_at:time")},
	{Name: "categories", OrderBy: "id", HasID: true, Columns: cols(
		"id:int", "name:text", "direction:text", "default_konto:text", "default_gegenkonto:text",
		"keywords:text", "default_vat_rate:int", "deductible_pct:int")},
	{Name: "recurring_invoices", OrderBy: "id", HasID: true, Columns: cols(
		"id:int", "contact_id:int", "name:text", "frequency:text", "next_due:text", "lines:text", "note:text",
		"active:int", "created_at:time")},
	{Name: "invoices", OrderBy: "id", HasID: true, Columns: cols(
		"id:int", "year:int", "seq:int", "number:text", "issue_date:text", "service_date:text",
		"due_date:text", "contact_id:int", "recipient_name:text", "recipient_address:text",
		"recipient_uid:text", "issuer_name:text", "issuer_address:text", "issuer_uid:text",
		"issuer_iban:text", "issuer_bic:text", "issuer_contact:text", "kleinunternehmer:int", "note:text", "net_cents:int", "vat_cents:int",
		"gross_cents:int", "created_at:time", "imported:int", "recurring_id:int")},
	{Name: "invoice_originals", OrderBy: "invoice_id", Columns: cols(
		"invoice_id:int", "filename:text", "content_type:text", "size_bytes:int", "sha256:text",
		"data:bytes", "created_at:time")},
	{Name: "invoice_lines", OrderBy: "id", HasID: true, Columns: cols(
		"id:int", "invoice_id:int", "position:int", "description:text", "quantity_milli:int",
		"unit:text", "unit_price_cents:int", "vat_rate:int", "net_cents:int", "vat_cents:int",
		"gross_cents:int")},
	{Name: "entries", OrderBy: "id", HasID: true, Columns: cols(
		"id:int", "booked_on:text", "direction:text", "description:text", "amount_gross_cents:int",
		"vat_rate:int", "vat_cents:int", "net_cents:int", "konto:text", "gegenkonto:text",
		"contact_id:int", "category_id:int", "reverses_entry_id:int", "invoice_id:int",
		"created_at:time", "business_pct:int", "business_note:text")},
	{Name: "entry_descriptions", OrderBy: "id", HasID: true, Columns: cols(
		"id:int", "entry_id:int", "description:text", "changed_at:time")},
	{Name: "entry_shares", OrderBy: "id", HasID: true, Columns: cols(
		"id:int", "entry_id:int", "business_pct:int", "business_note:text", "changed_at:time")},
	{Name: "assets", OrderBy: "id", HasID: true, Columns: cols(
		"id:int", "entry_id:int", "name:text", "cost_cents:int", "in_use_from:text", "useful_life_years:int",
		"created_at:time")},
	{Name: "bank_transactions", OrderBy: "id", HasID: true, Columns: cols(
		"id:int", "booked_on:text", "amount_cents:int", "counterparty:text", "counterparty_iban:text",
		"purpose:text", "raw_text:text", "row_hash:text", "source_name:text", "imported_at:time",
		"matched_entry_id:int", "private_at:time")},
	{Name: "bank_matches", OrderBy: "bank_transaction_id, entry_id", Columns: cols(
		"bank_transaction_id:int", "entry_id:int")},
	{Name: "invoice_drafts", OrderBy: "id", HasID: true, Columns: cols(
		"id:int", "data:text", "created_at:time", "updated_at:time")},
	{Name: "logo", OrderBy: "id", Columns: cols(
		"id:int", "content_type:text", "data:bytes", "updated_at:time")},
	{Name: "attachments", OrderBy: "id", HasID: true, Columns: cols(
		"id:int", "entry_id:int", "filename:text", "content_type:text", "size_bytes:int",
		"sha256:text", "data:bytes", "created_at:time")},
}

// derivedTables are left out of backups on purpose. The migrator owns
// app_meta and schema_migrations, and bank_suggestions is a cache of LLM
// proposals that can always be produced again (ADR 0020). Anything else that
// is not in backupTables makes CheckBackupCoverage fail.
var derivedTables = map[string]bool{
	"app_meta": true, "schema_migrations": true, "bank_suggestions": true,
	// Uploads waiting for review in a bulk import (ADR 0032): not part of
	// the books, and the originals are still on the user's disk.
	"invoice_import_queue": true,
	"receipt_queue":        true, // Belege waiting to be booked (ADR 0033)
	"notifications_sent":   true, // which reminders were mailed (ADR 0039)
	// Holds the mail password: left out so no backup or archive carries a
	// credential (ADR 0040). It is entered again after a restore.
	"mail_server": true,
}

type backupHeader struct {
	Format     string    `json:"format"`
	Version    int       `json:"version"`
	AppVersion string    `json:"app_version"`
	Engine     Dialect   `json:"engine"`
	CreatedAt  time.Time `json:"created_at"`
	Migrations []string  `json:"migrations"`
}

type tableStart struct {
	Table   string         `json:"table"`
	Columns []backupColumn `json:"columns"`
}

type tableEnd struct {
	End    string `json:"end"`
	Rows   int    `json:"rows"`
	SHA256 string `json:"sha256"`
}

type backupTrailer struct {
	Complete bool `json:"complete"`
	Tables   int  `json:"tables"`
}

// BackupSummary describes a backup that was written, verified or restored.
type BackupSummary struct {
	CreatedAt  time.Time
	AppVersion string
	Engine     Dialect
	Migrations []string
	Rows       map[string]int
}

// TotalRows is the number of rows over all tables.
func (b BackupSummary) TotalRows() int {
	n := 0
	for _, r := range b.Rows {
		n += r
	}
	return n
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// Backup writes a complete backup to w. It reads in one read-only
// transaction, so the file is a consistent snapshot even while the app is in
// use. The caller should call MarkBackup only once w has been safely stored.
func (s *Store) Backup(ctx context.Context, w io.Writer, appVersion string) (BackupSummary, error) {
	if err := s.CheckBackupCoverage(ctx); err != nil {
		return BackupSummary{}, err
	}
	migrations, err := s.appliedMigrations(ctx, s.DB)
	if err != nil {
		return BackupSummary{}, err
	}

	opts := &sql.TxOptions{ReadOnly: true}
	if s.Dialect == Postgres {
		// The default READ COMMITTED would let a booking committed halfway
		// through the dump appear in later tables but not in earlier ones.
		opts.Isolation = sql.LevelRepeatableRead
	}
	tx, err := s.DB.BeginTx(ctx, opts)
	if err != nil {
		return BackupSummary{}, err
	}
	defer tx.Rollback()

	gz := gzip.NewWriter(w)
	bw := bufio.NewWriterSize(gz, 64<<10)
	sum := BackupSummary{CreatedAt: time.Now().UTC(), AppVersion: appVersion, Engine: s.Dialect,
		Migrations: migrations, Rows: map[string]int{}}

	writeLine := func(v any) ([]byte, error) {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		b = append(b, '\n')
		_, err = bw.Write(b)
		return b, err
	}

	if _, err := writeLine(backupHeader{Format: BackupFormat, Version: backupVersion,
		AppVersion: appVersion, Engine: s.Dialect, CreatedAt: sum.CreatedAt, Migrations: migrations}); err != nil {
		return sum, err
	}
	for _, t := range backupTables {
		if _, err := writeLine(tableStart{Table: t.Name, Columns: t.Columns}); err != nil {
			return sum, err
		}
		n, hash, err := s.dumpTable(ctx, tx, t.Name, t.OrderBy, t.Columns, func(line []byte) error {
			_, err := bw.Write(line)
			return err
		})
		if err != nil {
			return sum, fmt.Errorf("back up %s: %w", t.Name, err)
		}
		sum.Rows[t.Name] = n
		if _, err := writeLine(tableEnd{End: t.Name, Rows: n, SHA256: hash}); err != nil {
			return sum, err
		}
	}
	if _, err := writeLine(backupTrailer{Complete: true, Tables: len(backupTables)}); err != nil {
		return sum, err
	}
	if err := bw.Flush(); err != nil {
		return sum, err
	}
	return sum, gz.Close()
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// dumpTable streams one table's rows as JSON lines and returns their count and
// the SHA-256 over the exact lines. The restore recomputes the same hash from
// the restored rows, which is how it proves nothing changed on the way.
func (s *Store) dumpTable(ctx context.Context, q queryer, table, orderBy string, columns []backupColumn,
	emit func(line []byte) error) (int, string, error) {

	names := make([]string, len(columns))
	for i, c := range columns {
		names[i] = quoteIdent(c.Name)
	}
	rows, err := q.QueryContext(ctx, "SELECT "+strings.Join(names, ", ")+
		" FROM "+quoteIdent(table)+" ORDER BY "+orderBy)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()

	h := sha256.New()
	n := 0
	dest := make([]any, len(columns))
	for i, c := range columns {
		switch c.Kind {
		case kindInt:
			dest[i] = new(sql.NullInt64)
		case kindText:
			dest[i] = new(sql.NullString)
		case kindTime:
			dest[i] = new(sql.NullTime)
		case kindBytes:
			dest[i] = new([]byte)
		default:
			return 0, "", fmt.Errorf("column %s: unknown kind %q", c.Name, c.Kind)
		}
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return n, "", err
		}
		vals := make([]any, len(columns))
		for i, d := range dest {
			switch v := d.(type) {
			case *sql.NullInt64:
				if v.Valid {
					vals[i] = v.Int64
				}
			case *sql.NullString:
				if v.Valid {
					vals[i] = v.String
				}
			case *sql.NullTime:
				if v.Valid {
					vals[i] = encodeTime(v.Time)
				}
			case *[]byte:
				if *v != nil {
					vals[i] = append([]byte(nil), *v...) // json encodes []byte as base64
				}
			}
		}
		line, err := json.Marshal(vals)
		if err != nil {
			return n, "", err
		}
		line = append(line, '\n')
		h.Write(line)
		if err := emit(line); err != nil {
			return n, "", err
		}
		n++
	}
	return n, hex.EncodeToString(h.Sum(nil)), rows.Err()
}

// encodeTime is the one canonical form for timestamps: UTC, microseconds.
// Postgres stores microseconds and SQLite whatever it is given, so without the
// truncation a SQLite → Postgres restore could not reproduce the hash.
func encodeTime(t time.Time) string {
	return t.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
}

func (s *Store) appliedMigrations(ctx context.Context, q queryer) ([]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT name FROM schema_migrations ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkBackup records when the last complete backup was taken. The overview
// warns when it is old (ADR 0019).
func (s *Store) MarkBackup(ctx context.Context, at time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, s.Rebind(`DELETE FROM settings WHERE "key" = ?`), "last_backup_at"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.Rebind(`INSERT INTO settings ("key", value) VALUES (?, ?)`),
		"last_backup_at", at.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

// LastBackup returns when the last backup was taken, or the zero time.
func (s *Store) LastBackup(ctx context.Context) (time.Time, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, s.Rebind(`SELECT value FROM settings WHERE "key" = ?`), "last_backup_at").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, nil
	}
	return t, nil
}

// backupReader reads a backup file line by line and checks its structure.
type backupReader struct {
	sc     *bufio.Scanner
	line   []byte
	lineNo int
}

func newBackupReader(r io.Reader) (*backupReader, func() error, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, nil, fmt.Errorf("not a backup file (no gzip data): %w", err)
	}
	sc := bufio.NewScanner(gz)
	// A row with an attachment holds up to 10 MiB, base64-encoded.
	sc.Buffer(make([]byte, 0, 1<<20), 32<<20)
	return &backupReader{sc: sc}, gz.Close, nil
}

func (br *backupReader) next() ([]byte, error) {
	if !br.sc.Scan() {
		if err := br.sc.Err(); err != nil {
			return nil, fmt.Errorf("line %d: %w", br.lineNo+1, err)
		}
		return nil, io.ErrUnexpectedEOF
	}
	br.lineNo++
	br.line = br.sc.Bytes()
	return br.line, nil
}

func (br *backupReader) header() (backupHeader, error) {
	line, err := br.next()
	if err != nil {
		return backupHeader{}, fmt.Errorf("backup header: %w", err)
	}
	var h backupHeader
	if err := json.Unmarshal(line, &h); err != nil || h.Format != BackupFormat {
		return backupHeader{}, fmt.Errorf("not a kmu-buchhaltung backup")
	}
	if h.Version != backupVersion {
		return backupHeader{}, fmt.Errorf("backup format version %d is not supported by this version of the app", h.Version)
	}
	return h, nil
}

// readBackup walks the whole file after the header and calls onRow for every
// row. It checks each table's row count and hash, and requires the trailer, so
// a truncated or altered file is an error rather than a partial restore.
func (br *backupReader) readTables(onTable func(tableStart) error, onRow func(tableStart, []any) error) (map[string]tableEnd, error) {
	ends := map[string]tableEnd{}
	for {
		line, err := br.next()
		if err != nil {
			return nil, fmt.Errorf("backup ends early, the file is incomplete: %w", err)
		}
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(line, &probe); err != nil {
			return nil, fmt.Errorf("line %d: %w", br.lineNo, err)
		}
		if _, ok := probe["complete"]; ok {
			var t backupTrailer
			if err := json.Unmarshal(line, &t); err != nil || !t.Complete || t.Tables != len(ends) {
				return nil, fmt.Errorf("line %d: the trailer does not match the tables read", br.lineNo)
			}
			return ends, nil
		}
		var ts tableStart
		if err := json.Unmarshal(line, &ts); err != nil || ts.Table == "" {
			return nil, fmt.Errorf("line %d: expected a table start", br.lineNo)
		}
		if err := onTable(ts); err != nil {
			return nil, err
		}

		h := sha256.New()
		n := 0
		for {
			line, err := br.next()
			if err != nil {
				return nil, fmt.Errorf("table %s ends early, the file is incomplete: %w", ts.Table, err)
			}
			if len(line) > 0 && line[0] == '{' {
				var te tableEnd
				if err := json.Unmarshal(line, &te); err != nil || te.End != ts.Table {
					return nil, fmt.Errorf("line %d: expected the end of table %s", br.lineNo, ts.Table)
				}
				if te.Rows != n || te.SHA256 != hex.EncodeToString(h.Sum(nil)) {
					return nil, fmt.Errorf("table %s is damaged: %d rows read, %d expected, or its checksum differs",
						ts.Table, n, te.Rows)
				}
				ends[ts.Table] = te
				break
			}
			h.Write(line)
			h.Write([]byte{'\n'})
			n++
			if onRow != nil {
				vals, err := decodeRow(line, ts.Columns)
				if err != nil {
					return nil, fmt.Errorf("line %d (%s): %w", br.lineNo, ts.Table, err)
				}
				if err := onRow(ts, vals); err != nil {
					return nil, err
				}
			}
		}
	}
}

func decodeRow(line []byte, columns []backupColumn) ([]any, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}
	if len(raw) != len(columns) {
		return nil, fmt.Errorf("%d values for %d columns", len(raw), len(columns))
	}
	out := make([]any, len(raw))
	for i, r := range raw {
		if string(r) == "null" {
			continue
		}
		switch columns[i].Kind {
		case kindInt:
			var v int64
			if err := json.Unmarshal(r, &v); err != nil {
				return nil, fmt.Errorf("%s: %w", columns[i].Name, err)
			}
			out[i] = v
		case kindText:
			var v string
			if err := json.Unmarshal(r, &v); err != nil {
				return nil, fmt.Errorf("%s: %w", columns[i].Name, err)
			}
			out[i] = v
		case kindTime:
			var v string
			if err := json.Unmarshal(r, &v); err != nil {
				return nil, fmt.Errorf("%s: %w", columns[i].Name, err)
			}
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", columns[i].Name, err)
			}
			out[i] = t.UTC()
		case kindBytes:
			var v []byte
			if err := json.Unmarshal(r, &v); err != nil {
				return nil, fmt.Errorf("%s: %w", columns[i].Name, err)
			}
			out[i] = v
		default:
			return nil, fmt.Errorf("column %s: unknown kind %q", columns[i].Name, columns[i].Kind)
		}
	}
	return out, nil
}

// VerifyBackup reads a whole backup file and checks its structure, row counts
// and checksums without touching any database.
func VerifyBackup(r io.Reader) (BackupSummary, error) {
	br, closeFn, err := newBackupReader(r)
	if err != nil {
		return BackupSummary{}, err
	}
	defer closeFn()
	h, err := br.header()
	if err != nil {
		return BackupSummary{}, err
	}
	ends, err := br.readTables(func(tableStart) error { return nil }, func(ts tableStart, _ []any) error { return nil })
	if err != nil {
		return BackupSummary{}, err
	}
	sum := BackupSummary{CreatedAt: h.CreatedAt, AppVersion: h.AppVersion, Engine: h.Engine,
		Migrations: h.Migrations, Rows: map[string]int{}}
	for t, e := range ends {
		sum.Rows[t] = e.Rows
	}
	return sum, nil
}

// ErrNotEmpty is returned when a restore target already holds data.
var ErrNotEmpty = errors.New("the target database is not empty")

// Restore loads a backup into the database named by dsn, which must be empty:
// a new SQLite file, or a new Postgres database or schema.
//
// The target is migrated only as far as the backup's own schema, the rows go
// in, and then the remaining migrations run. So a backup made by an older
// version of the app is restored through the same migrations its live database
// would have gone through. Everything is inserted in one transaction, the
// restored rows are re-read and hashed before it commits, and any difference
// to the file rolls the whole restore back.
func Restore(ctx context.Context, dsn string, r io.Reader) (BackupSummary, error) {
	br, closeFn, err := newBackupReader(r)
	if err != nil {
		return BackupSummary{}, err
	}
	defer closeFn()
	h, err := br.header()
	if err != nil {
		return BackupSummary{}, err
	}
	if len(h.Migrations) == 0 {
		return BackupSummary{}, fmt.Errorf("the backup lists no schema migrations")
	}

	s, err := openMigrated(dsn, h.Migrations[len(h.Migrations)-1])
	if err != nil {
		return BackupSummary{}, err
	}
	defer s.Close()

	known, err := s.embeddedMigrations()
	if err != nil {
		return BackupSummary{}, err
	}
	for i, m := range h.Migrations {
		if i >= len(known) || known[i] != m {
			return BackupSummary{}, fmt.Errorf("the backup was made by a newer or different version of the app (migration %s is unknown here)", m)
		}
	}
	applied, err := s.appliedMigrations(ctx, s.DB)
	if err != nil {
		return BackupSummary{}, err
	}
	if len(applied) != len(h.Migrations) {
		return BackupSummary{}, fmt.Errorf("%w: it is already at schema %s, the backup at %s — restore into a new, empty database",
			ErrNotEmpty, applied[len(applied)-1], h.Migrations[len(h.Migrations)-1])
	}
	if err := s.requireEmpty(ctx); err != nil {
		return BackupSummary{}, err
	}

	sum, err := s.loadBackup(ctx, br, h, nil)
	if err != nil {
		return BackupSummary{}, err
	}

	// Bring an older backup up to this version's schema.
	s.upTo = ""
	if err := s.migrate(); err != nil {
		return sum, fmt.Errorf("restored, but migrating it to the current schema failed: %w", err)
	}
	return sum, nil
}

// loadBackup inserts the backup's rows in one transaction, reads them back
// and compares them with the file, and commits only if every table matches.
// prepare, when set, runs first inside the same transaction.
func (s *Store) loadBackup(ctx context.Context, br *backupReader, h backupHeader, prepare func(*sql.Tx) error) (BackupSummary, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return BackupSummary{}, err
	}
	defer tx.Rollback()
	if prepare != nil {
		if err := prepare(tx); err != nil {
			return BackupSummary{}, err
		}
	}

	var (
		stmt   *sql.Stmt
		tables []tableStart
	)
	ends, err := br.readTables(func(ts tableStart) error {
		if stmt != nil {
			stmt.Close()
		}
		names := make([]string, len(ts.Columns))
		marks := make([]string, len(ts.Columns))
		for i, c := range ts.Columns {
			names[i], marks[i] = quoteIdent(c.Name), "?"
		}
		tables = append(tables, ts)
		stmt, err = tx.PrepareContext(ctx, s.Rebind("INSERT INTO "+quoteIdent(ts.Table)+
			" ("+strings.Join(names, ", ")+") VALUES ("+strings.Join(marks, ", ")+")"))
		if err != nil {
			return fmt.Errorf("table %s does not fit this database: %w", ts.Table, err)
		}
		return nil
	}, func(ts tableStart, vals []any) error {
		if _, err := stmt.ExecContext(ctx, vals...); err != nil {
			return fmt.Errorf("restore %s: %w", ts.Table, err)
		}
		return nil
	})
	if stmt != nil {
		stmt.Close()
	}
	if err != nil {
		return BackupSummary{}, err
	}

	// Read back what went in and compare it with the file, before committing.
	sum := BackupSummary{CreatedAt: h.CreatedAt, AppVersion: h.AppVersion, Engine: h.Engine,
		Migrations: h.Migrations, Rows: map[string]int{}}
	for _, ts := range tables {
		orderBy := "1"
		for _, t := range backupTables {
			if t.Name == ts.Table {
				orderBy = t.OrderBy
			}
		}
		n, hash, err := s.dumpTable(ctx, tx, ts.Table, orderBy, ts.Columns, func([]byte) error { return nil })
		if err != nil {
			return BackupSummary{}, fmt.Errorf("verify %s: %w", ts.Table, err)
		}
		if want := ends[ts.Table]; n != want.Rows || hash != want.SHA256 {
			return BackupSummary{}, fmt.Errorf("verify %s: the restored rows differ from the backup (%d rows, %d expected)",
				ts.Table, n, want.Rows)
		}
		sum.Rows[ts.Table] = n
	}

	if s.Dialect == Postgres {
		// Rows went in with their own ids, which identity columns do not
		// track. Without this, the next booking would collide with id 1.
		for _, t := range backupTables {
			if !t.HasID || sum.Rows[t.Name] == 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(
				`SELECT setval(pg_get_serial_sequence('%s', 'id'), (SELECT MAX(id) FROM %s))`,
				t.Name, quoteIdent(t.Name))); err != nil {
				return BackupSummary{}, fmt.Errorf("reset id sequence of %s: %w", t.Name, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return BackupSummary{}, err
	}
	return sum, nil
}

// requireEmpty refuses a target that holds any data. A restore merged into
// existing books would produce exactly the duplicates and gaps a backup exists
// to prevent.
func (s *Store) requireEmpty(ctx context.Context) error {
	for _, t := range backupTables {
		var n int
		err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quoteIdent(t.Name)).Scan(&n)
		if err != nil {
			// The table does not exist yet at the backup's schema version.
			continue
		}
		if n > 0 {
			return fmt.Errorf("%w: table %s has %d rows", ErrNotEmpty, t.Name, n)
		}
	}
	return nil
}

// CheckBackupCoverage compares backupTables with the live schema and fails if
// a table or column would be left out of a backup, or is listed but missing.
// Backup runs it first, so a migration that forgets to extend backupTables
// stops backups with a clear error instead of producing incomplete ones.
func (s *Store) CheckBackupCoverage(ctx context.Context) error {
	live, err := s.liveSchema(ctx)
	if err != nil {
		return fmt.Errorf("read schema: %w", err)
	}
	var problems []string
	listed := map[string]bool{}
	for _, t := range backupTables {
		listed[t.Name] = true
		cols, ok := live[t.Name]
		if !ok {
			problems = append(problems, "table "+t.Name+" does not exist")
			continue
		}
		want := map[string]bool{}
		for _, c := range t.Columns {
			want[c.Name] = true
			if !cols[c.Name] {
				problems = append(problems, t.Name+"."+c.Name+" does not exist")
			}
		}
		for c := range cols {
			if !want[c] {
				problems = append(problems, t.Name+"."+c+" is not backed up")
			}
		}
	}
	for t := range live {
		if !listed[t] && !derivedTables[t] {
			problems = append(problems, "table "+t+" is not backed up")
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("backup does not match the schema: %s", strings.Join(problems, "; "))
	}
	return nil
}

func (s *Store) liveSchema(ctx context.Context) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	add := func(t, c string) {
		if out[t] == nil {
			out[t] = map[string]bool{}
		}
		out[t][c] = true
	}
	if s.Dialect == Postgres {
		rows, err := s.DB.QueryContext(ctx, `SELECT table_name, column_name FROM information_schema.columns
			WHERE table_schema = current_schema()`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var t, c string
			if err := rows.Scan(&t, &c); err != nil {
				return nil, err
			}
			add(t, c)
		}
		return out, rows.Err()
	}

	rows, err := s.DB.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return nil, err
		}
		tables = append(tables, t)
	}
	rows.Close()
	for _, t := range tables {
		cr, err := s.DB.QueryContext(ctx, "SELECT name FROM pragma_table_info(?)", t)
		if err != nil {
			return nil, err
		}
		for cr.Next() {
			var c string
			if err := cr.Scan(&c); err != nil {
				cr.Close()
				return nil, err
			}
			add(t, c)
		}
		cr.Close()
	}
	return out, nil
}
