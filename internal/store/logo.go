package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Logo is the issuer's logo for invoice PDFs (ADR 0025).
type Logo struct {
	ContentType string // image/png or image/jpeg
	Data        []byte
	UpdatedAt   time.Time
}

// MaxLogoBytes keeps the logo small: it is embedded in every invoice PDF.
const MaxLogoBytes = 1 << 20

// GetLogo returns the logo; ok is false when none is set.
func (s *Store) GetLogo(ctx context.Context) (Logo, bool, error) {
	var l Logo
	err := s.DB.QueryRowContext(ctx, `SELECT content_type, data, updated_at FROM logo WHERE id = 1`).
		Scan(&l.ContentType, &l.Data, &l.UpdatedAt)
	if err == sql.ErrNoRows {
		return Logo{}, false, nil
	}
	return l, err == nil, err
}

// SaveLogo replaces the logo. The caller checks that the image can be drawn.
func (s *Store) SaveLogo(ctx context.Context, contentType string, data []byte) error {
	if contentType != "image/png" && contentType != "image/jpeg" {
		return fmt.Errorf("the logo must be a PNG or JPEG image")
	}
	if len(data) == 0 || len(data) > MaxLogoBytes {
		return fmt.Errorf("the logo must be at most 1 MiB")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM logo WHERE id = 1`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.Rebind(`INSERT INTO logo (id, content_type, data, updated_at) VALUES (1, ?, ?, ?)`),
		contentType, data, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteLogo removes the logo; later PDFs are drawn without one.
func (s *Store) DeleteLogo(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM logo WHERE id = 1`)
	return err
}
