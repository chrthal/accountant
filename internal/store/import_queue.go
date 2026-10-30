package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Queue names a waiting list of a bulk import: uploaded files with what was
// read from them, kept until the user imports or discards them. Both lists
// work alike and are left out of backups (ADR 0032, ADR 0033).
type Queue string

const (
	// InvoiceQueue holds invoices from another tool (ADR 0032). Fields:
	// number, issue_date, contact_id, new_contact_name, gross, vat_rate, note.
	InvoiceQueue Queue = "invoice_import_queue"
	// ReceiptQueue holds Belege to be booked (ADR 0033). Fields: booked_on,
	// direction, description, amount, vat_rate, category_id, contact_id,
	// sha256, note.
	ReceiptQueue Queue = "receipt_queue"
)

// QueuedImport is one file waiting on a Queue.
type QueuedImport struct {
	ID        int64
	Filename  string
	Fields    map[string]string
	CreatedAt time.Time
}

// MaxQueuedImports bounds each waiting list, so a bulk upload cannot fill the
// database by accident.
const MaxQueuedImports = 100

// QueueImport puts an uploaded original on the waiting list.
func (s *Store) QueueImport(ctx context.Context, q Queue, filename string, data []byte, fields map[string]string) (int64, error) {
	f, err := json.Marshal(fields)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.DB.QueryRowContext(ctx, s.Rebind(`INSERT INTO `+string(q)+` (filename, data, fields, created_at)
		VALUES (?, ?, ?, ?) RETURNING id`), filename, data, string(f), time.Now().UTC()).Scan(&id)
	return id, err
}

// ImportQueue lists the waiting list in upload order, without the files.
func (s *Store) ImportQueue(ctx context.Context, q Queue) ([]QueuedImport, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, filename, fields, created_at FROM `+string(q)+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QueuedImport
	for rows.Next() {
		var (
			item QueuedImport
			f    string
		)
		if err := rows.Scan(&item.ID, &item.Filename, &f, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(f), &item.Fields); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// QueuedOriginal returns one waiting invoice with its file.
func (s *Store) QueuedOriginal(ctx context.Context, q Queue, id int64) (QueuedImport, []byte, error) {
	var (
		item QueuedImport
		f    string
		data []byte
	)
	err := s.DB.QueryRowContext(ctx, s.Rebind(`SELECT id, filename, fields, created_at, data FROM `+string(q)+` WHERE id = ?`), id).
		Scan(&item.ID, &item.Filename, &f, &item.CreatedAt, &data)
	if err == sql.ErrNoRows {
		return QueuedImport{}, nil, ErrNotFound
	}
	if err != nil {
		return QueuedImport{}, nil, err
	}
	return item, data, json.Unmarshal([]byte(f), &item.Fields)
}

// UpdateQueuedFields keeps what the user typed in the review, so nothing is
// lost when some rows fail.
func (s *Store) UpdateQueuedFields(ctx context.Context, q Queue, id int64, fields map[string]string) error {
	f, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, s.Rebind(`UPDATE `+string(q)+` SET fields = ? WHERE id = ?`), string(f), id)
	return err
}

// DiscardQueuedImport removes one waiting invoice; DiscardImportQueue all.
func (s *Store) DiscardQueuedImport(ctx context.Context, q Queue, id int64) error {
	_, err := s.DB.ExecContext(ctx, s.Rebind(`DELETE FROM `+string(q)+` WHERE id = ?`), id)
	return err
}

func (s *Store) DiscardImportQueue(ctx context.Context, q Queue) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM `+string(q))
	return err
}

// InvoiceNumbers returns every number in use, for spotting duplicates
// before an import.
func (s *Store) InvoiceNumbers(ctx context.Context) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT number FROM invoices`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out[n] = true
	}
	return out, rows.Err()
}
