package notify

import (
	"bufio"
	"context"
	"io"
	"mime/quotedprintable"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chrthal/accountant/internal/store"
)

type fakeMailer struct {
	sent []struct{ to, subject, body string }
}

func (f *fakeMailer) Send(_ context.Context, to, subject, body string) error {
	f.sent = append(f.sent, struct{ to, subject, body string }{to, subject, body})
	return nil
}

func setup(t *testing.T) (*store.Store, int64) {
	t.Helper()
	st, err := store.Open("sqlite://" + t.TempDir() + "/books.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.SaveSettings(ctx, store.Settings{IssuerName: "Mustermann IT", NotifyEmail: "ich@example.at",
		NotifyRecurring: true, NotifyOverdue: true, NotifyBackup: true, NotifyQueues: true}); err != nil {
		t.Fatal(err)
	}
	id, err := st.CreateContact(ctx, store.Contact{Kind: "customer", Name: "Muster GmbH"})
	if err != nil {
		t.Fatal(err)
	}
	return st, id
}

// ADR 0039: new events go out in one mail, each only once.
func TestNotifierMailsEachEventOnce(t *testing.T) {
	st, contact := setup(t)
	ctx := context.Background()
	now := time.Now()
	day := func(d int) string { return now.AddDate(0, 0, d).Format("2006-01-02") }

	if _, err := st.SaveRecurring(ctx, store.Recurring{ContactID: contact, Name: "Hosting", Interval: "monthly", NextDue: day(3), Active: true,
		Lines: []store.DraftLine{{Description: "Hosting", Quantity: "1", UnitPrice: "25,00", VATRate: "20"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateInvoice(ctx, store.NewInvoice{ContactID: contact, IssueDate: day(-30), ServiceDate: day(-30), DueDate: day(-2),
		Lines: []store.InvoiceLine{{Description: "Beratung", QuantityMilli: 1000, UnitPriceCents: 10000, VATRate: 20}}}); err != nil {
		t.Fatal(err)
	}

	m := &fakeMailer{}
	n := &Notifier{Store: st, Mailer: m, PublicURL: "https://buch.example.at/", now: func() time.Time { return now }}
	sent, err := n.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Due soon, overdue, and no backup yet for books that hold data.
	if sent != 3 || len(m.sent) != 1 {
		t.Fatalf("first run: %d events in %d mails, want 3 in 1", sent, len(m.sent))
	}
	mail := m.sent[0]
	for _, want := range []string{"Wiederkehrende Rechnung „Hosting“ für Muster GmbH ist am", "Rechnung 2026", "überfällig, offen 120,00 €",
		"Es gibt noch keine Datensicherung", "https://buch.example.at/invoices/new?recurring=1"} {
		if !strings.Contains(mail.body, want) {
			t.Errorf("mail lacks %q:\n%s", want, mail.body)
		}
	}
	if mail.to != "ich@example.at" || mail.subject != "kmu-buchhaltung: 3 Erinnerung(en)" {
		t.Errorf("mail to %q, subject %q", mail.to, mail.subject)
	}

	if sent, _ := n.RunOnce(ctx); sent != 0 {
		t.Errorf("second run mailed %d again", sent)
	}

	// Four days on, the recurring invoice is due: that is news; the overdue
	// invoice is not.
	n.now = func() time.Time { return now.AddDate(0, 0, 4) }
	n.RunOnce(ctx)
	last := m.sent[len(m.sent)-1].body
	if !strings.Contains(last, "ist fällig") || strings.Contains(last, "überfällig") {
		t.Errorf("four days later:\n%s", last)
	}

	// No address, no mail.
	s, _ := st.Settings(ctx)
	s.NotifyEmail = ""
	st.SaveSettings(ctx, s)
	n.now = func() time.Time { return now.AddDate(0, 1, 0) }
	before := len(m.sent)
	n.RunOnce(ctx)
	if len(m.sent) != before {
		t.Error("mailed without an address")
	}
}

// The mail is in the UI language.
func TestNotifierSpeaksEnglish(t *testing.T) {
	st, _ := setup(t)
	ctx := context.Background()
	s, _ := st.Settings(ctx)
	s.Language = "en"
	st.SaveSettings(ctx, s)
	m := &fakeMailer{}
	n := &Notifier{Store: st, Mailer: m}
	if _, err := n.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(m.sent) != 1 || !strings.Contains(m.sent[0].body, "There is no backup yet") {
		t.Errorf("mails = %+v", m.sent)
	}
}

// fakeSMTP speaks just enough SMTP to accept one mail; it offers STARTTLS
// only when told to.
func fakeSMTP(t *testing.T, offerTLS bool) (string, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r, w := bufio.NewReader(c), bufio.NewWriter(c)
		say := func(s string) { w.WriteString(s + "\r\n"); w.Flush() }
		say("220 fake")
		var data strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(cmd, "EHLO"):
				if offerTLS {
					say("250-fake\r\n250 STARTTLS")
				} else {
					say("250 fake")
				}
			case strings.HasPrefix(cmd, "DATA"):
				say("354 go")
				for {
					l, err := r.ReadString('\n')
					if err != nil || l == ".\r\n" {
						break
					}
					data.WriteString(l)
				}
				say("250 ok")
				got <- data.String()
			case strings.HasPrefix(cmd, "QUIT"):
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	return ln.Addr().String(), got
}

func TestSMTPSendsAProperMail(t *testing.T) {
	addr, got := fakeSMTP(t, false)
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	s := &SMTP{Host: host, Port: p, TLS: NoTLS, From: "buch@example.at"}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), "ich@example.at", "Rechnung überfällig", "Grüße\nZeile 2"); err != nil {
		t.Fatal(err)
	}
	raw := <-got
	head, body, _ := strings.Cut(raw, "\r\n\r\n")
	for _, want := range []string{"From: buch@example.at", "To: ich@example.at", "Subject: =?utf-8?q?Rechnung_=C3=BCberf=C3=A4llig?=",
		"Content-Type: text/plain; charset=utf-8", "Message-ID: <", "Auto-Submitted: auto-generated"} {
		if !strings.Contains(head, want) {
			t.Errorf("headers lack %q:\n%s", want, head)
		}
	}
	decoded, _ := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
	if !strings.Contains(string(decoded), "Grüße\r\nZeile 2") {
		t.Errorf("body = %q", decoded)
	}

	// STARTTLS required but not offered: refuse instead of logging in in clear.
	addr2, _ := fakeSMTP(t, false)
	host2, port2, _ := net.SplitHostPort(addr2)
	p2, _ := strconv.Atoi(port2)
	s2 := &SMTP{Host: host2, Port: p2, TLS: StartTLS, From: "buch@example.at", User: "u", Password: "p"}
	if err := s2.Send(context.Background(), "ich@example.at", "x", "y"); err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("without STARTTLS = %v", err)
	}
}

func TestUnencryptedOnlyToALocalRelay(t *testing.T) {
	if err := (&SMTP{Host: "mail.example.at", Port: 25, TLS: NoTLS, From: "a@b.at"}).check(); err == nil {
		t.Error("unencrypted mail to a remote server was allowed")
	}
	if err := (&SMTP{Host: "localhost", Port: 25, TLS: NoTLS, From: "a@b.at"}).check(); err != nil {
		t.Error(err)
	}
}
