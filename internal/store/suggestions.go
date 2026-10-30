package store

import (
	"context"
	"database/sql"
	"time"
)

// BankSuggestion is a booking proposal for an open bank line from the
// optional LLM (ADR 0020). It is only ever shown as a draft. Any of the
// fields may be empty, because the model's answer is validated and whatever
// does not hold up is dropped rather than kept.
type BankSuggestion struct {
	BankTransactionID int64
	Description       string
	CategoryID        *int64
	ContactID         *int64
	VATRate           *int
	Unsure            bool
	Model             string
	CreatedAt         time.Time
}

// SaveBankSuggestion stores or replaces the suggestion for a transaction.
func (s *Store) SaveBankSuggestion(ctx context.Context, sug BankSuggestion) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, s.Rebind(`DELETE FROM bank_suggestions WHERE bank_transaction_id = ?`),
		sug.BankTransactionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.Rebind(`INSERT INTO bank_suggestions
		(bank_transaction_id, description, category_id, contact_id, vat_rate, unsure, model, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`),
		sug.BankTransactionID, sug.Description, sug.CategoryID, sug.ContactID, sug.VATRate,
		boolInt(sug.Unsure), sug.Model, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

// BankSuggestions returns the stored suggestions of unmatched transactions,
// by transaction id.
func (s *Store) BankSuggestions(ctx context.Context) (map[int64]BankSuggestion, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT g.bank_transaction_id, g.description, g.category_id,
		g.contact_id, g.vat_rate, g.unsure, g.model, g.created_at
		FROM bank_suggestions g
		WHERE NOT EXISTS (SELECT 1 FROM bank_matches m WHERE m.bank_transaction_id = g.bank_transaction_id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]BankSuggestion{}
	for rows.Next() {
		var (
			g             BankSuggestion
			cat, con, vat sql.NullInt64
			unsure        int
		)
		if err := rows.Scan(&g.BankTransactionID, &g.Description, &cat, &con, &vat, &unsure, &g.Model, &g.CreatedAt); err != nil {
			return nil, err
		}
		if cat.Valid {
			g.CategoryID = &cat.Int64
		}
		if con.Valid {
			g.ContactID = &con.Int64
		}
		if vat.Valid {
			v := int(vat.Int64)
			g.VATRate = &v
		}
		g.Unsure = unsure == 1
		out[g.BankTransactionID] = g
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
