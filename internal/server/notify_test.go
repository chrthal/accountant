package server_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/notify"
	"github.com/chrthal/accountant/internal/server"
	"github.com/chrthal/accountant/internal/store"
)

type captureMailer struct {
	to, subject string
	account     *notify.SMTP
	fail        error
}

func (c *captureMailer) Send(_ context.Context, to, subject, _ string) error {
	if c.fail != nil {
		return c.fail
	}
	c.to, c.subject = to, subject
	return nil
}

func mailServer(t *testing.T, opts ...server.Option) (http.Handler, *store.Store, *captureMailer) {
	t.Helper()
	st, err := store.Open("sqlite://" + filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m := &captureMailer{}
	opts = append(opts, server.WithMailerFactory(func(a *notify.SMTP) notify.Mailer { m.account = a; return m }))
	return server.New(st, "test", opts...), st, m
}

// ADR 0040: the mail server is set and tried out in the web UI; the password
// is kept, never shown, and stays out of backups.
func TestMailServerInTheWebUI(t *testing.T) {
	h, st, m := mailServer(t)
	ctx := t.Context()
	form := url.Values{"issuer_name": {"Mustermann IT"}, "notify_email": {"ich@example.at"}, "notify_overdue": {"1"},
		"smtp_host": {"mail.example.at"}, "smtp_tls": {"starttls"}, "smtp_port": {""}, "smtp_user": {"buch@example.at"},
		"smtp_password": {"geheim-123"}, "smtp_from": {""}}
	if rec := post(t, h, "/settings", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body.String())
	}
	stored, ok, _ := st.GetMailServer(ctx)
	if !ok || stored.Port != 587 || stored.Password != "geheim-123" || stored.Host != "mail.example.at" {
		t.Fatalf("stored = %+v", stored)
	}
	page := get(t, h, "/settings").Body.String()
	if strings.Contains(page, "geheim-123") {
		t.Fatal("the password is on the page")
	}
	if !strings.Contains(page, `value="mail.example.at"`) || !strings.Contains(page, "gespeichert; leer lassen") {
		t.Error("the settings do not show the stored server")
	}

	// Saving again with an empty password keeps it.
	form.Set("smtp_password", "")
	post(t, h, "/settings", form)
	if stored, _, _ := st.GetMailServer(ctx); stored.Password != "geheim-123" {
		t.Error("an empty password field erased the password")
	}

	// The test mail takes what is typed, and the stored password.
	test := url.Values{"notify_email": {"neu@example.at"}, "smtp_host": {"smtp.other.at"}, "smtp_tls": {"tls"}, "smtp_user": {"x@other.at"}, "smtp_password": {""}}
	if rec := post(t, h, "/settings/notify-test", test); rec.Code != http.StatusSeeOther || m.to != "neu@example.at" {
		t.Fatalf("test mail = %d to %q", rec.Code, m.to)
	}
	if m.account.Host != "smtp.other.at" || m.account.Port != 465 || m.account.Password != "geheim-123" {
		t.Errorf("test mail account = %+v", m.account)
	}
	if stored, _, _ := st.GetMailServer(ctx); stored.Host != "mail.example.at" {
		t.Error("the test mail saved the typed server")
	}
	m.fail = errors.New("535 authentication failed")
	if rec := post(t, h, "/settings/notify-test", url.Values{"notify_email": {"neu@example.at"}}); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "535 authentication failed") {
		t.Errorf("failed test mail = %d", rec.Code)
	}

	// A broken server setting is refused, and nothing else is saved.
	bad := url.Values{"issuer_name": {"Neuer Name"}, "smtp_host": {"mail.example.at"}, "smtp_tls": {"none"}, "smtp_user": {"buch@example.at"}}
	if rec := post(t, h, "/settings", bad); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unencrypted to a remote server = %d", rec.Code)
	}
	if s, _ := st.Settings(ctx); s.IssuerName != "Mustermann IT" {
		t.Error("the other settings were saved although the mail server was refused")
	}

	// Backups carry no credential.
	backup := get(t, h, "/backup").Body.Bytes()
	if strings.Contains(string(gunzip(t, backup)), "geheim-123") {
		t.Error("the backup contains the mail password")
	}

	post(t, h, "/settings/mail-delete", url.Values{})
	if _, ok, _ := st.GetMailServer(ctx); ok {
		t.Error("the mail server was not removed")
	}
}

// From the environment, the server is shown read-only and used as it is.
func TestMailServerFromTheEnvironment(t *testing.T) {
	env := &captureMailer{}
	h, _, _ := mailServer(t, server.WithEnvMailer(env, "env.example.at:587 (starttls), buch@example.at"))
	post(t, h, "/settings", url.Values{"issuer_name": {"Mustermann IT"}, "notify_email": {"ich@example.at"}})
	page := get(t, h, "/settings").Body.String()
	if !strings.Contains(page, "env.example.at:587") || strings.Contains(page, `name="smtp_host"`) {
		t.Error("the environment's mail server must be shown read-only")
	}
	if rec := post(t, h, "/settings/notify-test", url.Values{}); rec.Code != http.StatusSeeOther || env.to != "ich@example.at" {
		t.Errorf("test mail via the environment = %d to %q", rec.Code, env.to)
	}
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
