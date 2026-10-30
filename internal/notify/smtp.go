// Package notify mails reminders to the owner of the books (ADR 0039): only
// to the one address set in the settings, never to a customer. The mail
// server is configured from the environment, so its password stays out of
// the database and the backups.
package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/chrthal/accountant/internal/store"
)

// Mailer sends one plain-text mail.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// TLS modes of a mail server.
const (
	StartTLS = "starttls" // plain connection upgraded with STARTTLS (port 587)
	TLS      = "tls"      // TLS from the start (port 465)
	NoTLS    = "none"     // unencrypted, only to a relay on this machine
)

// SMTP is a mail server account.
type SMTP struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
	TLS      string
	Timeout  time.Duration
}

// FromEnv reads the mail server from BOOKKEEPER_SMTP_*; ok is false when no
// host is set, which turns mail off.
//
//	BOOKKEEPER_SMTP_HOST, BOOKKEEPER_SMTP_PORT (587), BOOKKEEPER_SMTP_TLS
//	(starttls, tls, none), BOOKKEEPER_SMTP_USER, BOOKKEEPER_SMTP_PASSWORD or
//	BOOKKEEPER_SMTP_PASSWORD_FILE, BOOKKEEPER_SMTP_FROM (defaults to the user)
func FromEnv() (*SMTP, bool, error) {
	host := strings.TrimSpace(os.Getenv("BOOKKEEPER_SMTP_HOST"))
	if host == "" {
		return nil, false, nil
	}
	s := &SMTP{Host: host, Port: 587, TLS: StartTLS, User: os.Getenv("BOOKKEEPER_SMTP_USER"),
		Password: os.Getenv("BOOKKEEPER_SMTP_PASSWORD"), From: os.Getenv("BOOKKEEPER_SMTP_FROM"), Timeout: 30 * time.Second}
	if v := os.Getenv("BOOKKEEPER_SMTP_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			return nil, false, fmt.Errorf("BOOKKEEPER_SMTP_PORT must be a port number, got %q", v)
		}
		s.Port = p
		if p == 465 {
			s.TLS = TLS
		}
	}
	if v := os.Getenv("BOOKKEEPER_SMTP_TLS"); v != "" {
		s.TLS = v
	}
	if path := os.Getenv("BOOKKEEPER_SMTP_PASSWORD_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, false, fmt.Errorf("read BOOKKEEPER_SMTP_PASSWORD_FILE: %w", err)
		}
		s.Password = strings.TrimSpace(string(b))
	}
	if s.From == "" {
		s.From = s.User
	}
	return s, true, s.check()
}

// FromStore builds the account from a mail server set in the web UI.
func FromStore(m store.MailServer) *SMTP {
	s := &SMTP{Host: strings.TrimSpace(m.Host), Port: m.Port, TLS: m.TLS, User: strings.TrimSpace(m.Username),
		Password: m.Password, From: strings.TrimSpace(m.From), Timeout: 30 * time.Second}
	if s.From == "" {
		s.From = s.User
	}
	return s
}

// Resolve finds the mail server: the environment first (ADR 0039), then the
// one set in the web UI (ADR 0040). It returns nil when there is none.
// fromEnv reports where it came from.
func Resolve(ctx context.Context, st *store.Store) (s *SMTP, fromEnv bool, err error) {
	if env, ok, err := FromEnv(); ok || err != nil {
		return env, true, err
	}
	m, ok, err := st.GetMailServer(ctx)
	if err != nil || !ok {
		return nil, false, err
	}
	s = FromStore(m)
	return s, false, s.Check()
}

// Check reports what is wrong with the account, if anything.
func (s *SMTP) Check() error { return s.check() }

func (s *SMTP) check() error {
	if s.Host == "" {
		return fmt.Errorf("the mail server needs a host")
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("the mail server port must be between 1 and 65535")
	}
	switch s.TLS {
	case StartTLS, TLS:
	case NoTLS:
		// Unencrypted only to a relay on this machine: a password or the
		// books' reminders must not cross a network in clear text.
		if !isLoopback(s.Host) {
			return fmt.Errorf("unencrypted mail is only allowed to a mail server on this machine")
		}
	default:
		return fmt.Errorf("the encryption must be starttls, tls or none")
	}
	if _, err := mail.ParseAddress(s.From); err != nil {
		return fmt.Errorf("the sender (or, without one, the user) is not an email address")
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Describe names the server for the settings page, without the password.
func (s *SMTP) Describe() string {
	return fmt.Sprintf("%s:%d (%s), %s", s.Host, s.Port, s.TLS, s.From)
}

// Send delivers one mail.
func (s *SMTP) Send(ctx context.Context, to, subject, body string) error {
	if _, err := mail.ParseAddress(to); err != nil {
		return fmt.Errorf("not an email address: %q", to)
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	tlsConfig := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}

	var conn net.Conn
	var err error
	d := &net.Dialer{}
	if s.TLS == TLS {
		conn, err = (&tls.Dialer{NetDialer: d, Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect to the mail server: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mail server: %w", err)
	}
	defer c.Close()

	if s.TLS == StartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the mail server does not offer STARTTLS; refusing to log in without encryption")
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if s.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.User, s.Password, s.Host)); err != nil {
			return fmt.Errorf("mail server login: %w", err)
		}
	}
	from, _ := mail.ParseAddress(s.From)
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("mail server refused the sender: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("mail server refused the recipient: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(Message(s.From, to, subject, body, time.Now())); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail server did not accept the mail: %w", err)
	}
	return c.Quit()
}

// Message renders a plain-text mail: UTF-8, quoted-printable, with the
// headers a mail program expects.
func Message(from, to, subject, body string, at time.Time) []byte {
	var b bytes.Buffer
	id := make([]byte, 12)
	rand.Read(id)
	domain := "kmu-buchhaltung"
	if a, err := mail.ParseAddress(from); err == nil {
		if i := strings.LastIndex(a.Address, "@"); i >= 0 {
			domain = a.Address[i+1:]
		}
	}
	for _, h := range [][2]string{
		{"From", from},
		{"To", to},
		{"Subject", mime.QEncoding.Encode("utf-8", subject)},
		{"Date", at.Format(time.RFC1123Z)},
		{"Message-ID", "<" + hex.EncodeToString(id) + "@" + domain + ">"},
		{"MIME-Version", "1.0"},
		{"Content-Type", "text/plain; charset=utf-8"},
		{"Content-Transfer-Encoding", "quoted-printable"},
		{"Auto-Submitted", "auto-generated"},
	} {
		b.WriteString(h[0] + ": " + h[1] + "\r\n")
	}
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	qp.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
	qp.Close()
	return b.Bytes()
}
