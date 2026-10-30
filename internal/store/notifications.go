package store

import (
	"context"
	"time"
)

// NotifiedKeys returns which of keys were mailed already (ADR 0039).
func (s *Store) NotifiedKeys(ctx context.Context, keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, k := range keys {
		var n int
		if err := s.DB.QueryRowContext(ctx, s.Rebind(`SELECT COUNT(*) FROM notifications_sent WHERE "key" = ?`), k).Scan(&n); err != nil {
			return nil, err
		}
		out[k] = n > 0
	}
	return out, nil
}

// MarkNotified records keys as mailed.
func (s *Store) MarkNotified(ctx context.Context, keys []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	for _, k := range keys {
		if _, err := tx.ExecContext(ctx, s.Rebind(`DELETE FROM notifications_sent WHERE "key" = ?`), k); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.Rebind(`INSERT INTO notifications_sent ("key", sent_at) VALUES (?, ?)`), k, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}
