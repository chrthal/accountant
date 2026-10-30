package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// InvoiceDraft is an invoice that has not been issued yet (ADR 0026). It
// keeps the form as typed, as text, so half-finished input survives.
type InvoiceDraft struct {
	ID        int64
	Fields    map[string]string // contact_id, issue_date, service_date, due_date, note
	Lines     []DraftLine
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DraftLine is one position as typed.
type DraftLine struct {
	Description string `json:"description"`
	Quantity    string `json:"quantity"`
	Unit        string `json:"unit"`
	UnitPrice   string `json:"unit_price"`
	VATRate     string `json:"vat_rate"`
}

type draftData struct {
	Fields map[string]string `json:"fields"`
	Lines  []DraftLine       `json:"lines"`
}

// SaveDraft creates a draft when id is 0, and replaces draft id otherwise.
// It returns the draft's id.
func (s *Store) SaveDraft(ctx context.Context, id int64, fields map[string]string, lines []DraftLine) (int64, error) {
	data, err := json.Marshal(draftData{Fields: fields, Lines: lines})
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	if id == 0 {
		err := s.DB.QueryRowContext(ctx, s.Rebind(`INSERT INTO invoice_drafts (data, created_at, updated_at)
			VALUES (?, ?, ?) RETURNING id`), string(data), now, now).Scan(&id)
		return id, err
	}
	res, err := s.DB.ExecContext(ctx, s.Rebind(`UPDATE invoice_drafts SET data = ?, updated_at = ? WHERE id = ?`),
		string(data), now, id)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrNotFound
	}
	return id, nil
}

// GetDraft returns one draft.
func (s *Store) GetDraft(ctx context.Context, id int64) (InvoiceDraft, error) {
	d, err := scanDraft(s.DB.QueryRowContext(ctx,
		s.Rebind(`SELECT id, data, created_at, updated_at FROM invoice_drafts WHERE id = ?`), id))
	if err == sql.ErrNoRows {
		return InvoiceDraft{}, ErrNotFound
	}
	return d, err
}

// ListDrafts returns the drafts, most recently changed first.
func (s *Store) ListDrafts(ctx context.Context) ([]InvoiceDraft, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, data, created_at, updated_at FROM invoice_drafts ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InvoiceDraft
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteDraft removes a draft. Deleting one that is gone is not an error.
func (s *Store) DeleteDraft(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, s.Rebind(`DELETE FROM invoice_drafts WHERE id = ?`), id)
	return err
}

func scanDraft(row interface{ Scan(...any) error }) (InvoiceDraft, error) {
	var (
		d    InvoiceDraft
		data string
	)
	if err := row.Scan(&d.ID, &data, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return InvoiceDraft{}, err
	}
	var dd draftData
	if err := json.Unmarshal([]byte(data), &dd); err != nil {
		return InvoiceDraft{}, err
	}
	d.Fields, d.Lines = dd.Fields, dd.Lines
	if d.Fields == nil {
		d.Fields = map[string]string{}
	}
	return d, nil
}
