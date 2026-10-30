package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// MailServer is the SMTP account for notifications, when set in the web UI
// (ADR 0040). Environment variables take precedence over it.
type MailServer struct {
	Host     string
	Port     int
	TLS      string // starttls, tls or none
	Username string
	Password string
	From     string
}

// GetMailServer returns the stored mail server; ok is false when none is set.
func (s *Store) GetMailServer(ctx context.Context) (MailServer, bool, error) {
	var m MailServer
	err := s.DB.QueryRowContext(ctx, `SELECT host, port, tls, username, password, from_addr FROM mail_server WHERE id = 1`).
		Scan(&m.Host, &m.Port, &m.TLS, &m.Username, &m.Password, &m.From)
	if errors.Is(err, sql.ErrNoRows) {
		return MailServer{}, false, nil
	}
	return m, err == nil, err
}

// SaveMailServer replaces the stored mail server. The caller has checked it.
func (s *Store) SaveMailServer(ctx context.Context, m MailServer) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM mail_server WHERE id = 1`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.Rebind(`INSERT INTO mail_server (id, host, port, tls, username, password, from_addr, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?)`), m.Host, m.Port, m.TLS, m.Username, m.Password, m.From, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteMailServer removes the stored mail server, password included.
func (s *Store) DeleteMailServer(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM mail_server WHERE id = 1`)
	return err
}
