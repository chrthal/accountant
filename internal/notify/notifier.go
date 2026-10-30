package notify

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/chrthal/accountant/internal/i18n"
	"github.com/chrthal/accountant/internal/money"
	"github.com/chrthal/accountant/internal/store"
)

// Notifier looks for things the owner should know about and mails new ones,
// all in one mail per run (ADR 0039). Each event has a key; a key that was
// mailed is not mailed again, so a reminder arrives once, not every hour.
type Notifier struct {
	Store *store.Store
	// Mailer sends; when nil, Resolve finds the mail server on every run,
	// so a server set in the web UI applies without a restart.
	Mailer    Mailer
	Resolve   func(context.Context) (Mailer, error)
	PublicURL string // e.g. https://buchhaltung.example.at; links are left out without it
	Every     time.Duration
	Log       *log.Logger
	now       func() time.Time
}

// Event is one thing to tell.
type Event struct {
	Key  string
	Text string
	Link string // a path in the app, e.g. /invoices/3
}

// recurringSoonDays matches the reminder on the overview (ADR 0038).
const recurringSoonDays = 7

// backupStaleAfter is how old the last backup may get before a reminder.
const backupStaleAfter = 7 * 24 * time.Hour

// Run checks once at start and then every interval until ctx ends.
func (n *Notifier) Run(ctx context.Context) {
	every := n.Every
	if every == 0 {
		every = time.Hour
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if sent, err := n.RunOnce(ctx); err != nil {
			n.logf("notify: %v", err)
		} else if sent > 0 {
			n.logf("notify: mailed %d reminder(s)", sent)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunOnce mails the events not mailed yet and reports how many it mailed.
func (n *Notifier) RunOnce(ctx context.Context) (int, error) {
	settings, err := n.Store.Settings(ctx)
	if err != nil {
		return 0, err
	}
	if settings.NotifyEmail == "" {
		return 0, nil
	}
	mailer, err := n.mailer(ctx)
	if err != nil || mailer == nil {
		return 0, err
	}
	events, err := n.Events(ctx, settings)
	if err != nil {
		return 0, err
	}
	keys := make([]string, len(events))
	for i, e := range events {
		keys[i] = e.Key
	}
	done, err := n.Store.NotifiedKeys(ctx, keys)
	if err != nil {
		return 0, err
	}
	var fresh []Event
	for _, e := range events {
		if !done[e.Key] {
			fresh = append(fresh, e)
		}
	}
	if len(fresh) == 0 {
		return 0, nil
	}
	subject, body := n.Compose(settings, fresh)
	if err := mailer.Send(ctx, settings.NotifyEmail, subject, body); err != nil {
		return 0, err
	}
	sent := make([]string, len(fresh))
	for i, e := range fresh {
		sent[i] = e.Key
	}
	return len(fresh), n.Store.MarkNotified(ctx, sent)
}

// NotifyBackupFailed mails a failed automatic backup at once, once a day.
func (n *Notifier) NotifyBackupFailed(ctx context.Context, cause error) {
	settings, err := n.Store.Settings(ctx)
	if err != nil || settings.NotifyEmail == "" || !settings.NotifyBackup {
		return
	}
	lang := settings.Language
	e := Event{Key: "backup-failed:" + n.clock().Format("2006-01-02"),
		Text: i18n.Tf(lang, "Die automatische Datensicherung ist fehlgeschlagen: %s", cause.Error()), Link: "/settings#backup"}
	if done, err := n.Store.NotifiedKeys(ctx, []string{e.Key}); err != nil || done[e.Key] {
		return
	}
	mailer, err := n.mailer(ctx)
	if err != nil || mailer == nil {
		return
	}
	subject, body := n.Compose(settings, []Event{e})
	if err := mailer.Send(ctx, settings.NotifyEmail, subject, body); err != nil {
		n.logf("notify: backup failure: %v", err)
		return
	}
	n.Store.MarkNotified(ctx, []string{e.Key})
}

// Events lists what is worth telling now, by the kinds switched on.
func (n *Notifier) Events(ctx context.Context, settings store.Settings) ([]Event, error) {
	lang, now := settings.Language, n.clock()
	today := now.Format("2006-01-02")
	date := func(iso string) string {
		d, err := time.Parse("2006-01-02", iso)
		if err != nil {
			return iso
		}
		return d.Format(settings.DateFormat)
	}
	eur := func(c int64) string { return money.FormatLang(c, lang) + " €" }
	var out []Event

	if settings.NotifyRecurring {
		due, err := n.Store.DueRecurring(ctx, now, recurringSoonDays)
		if err != nil {
			return nil, err
		}
		for _, r := range due {
			link := fmt.Sprintf("/invoices/new?recurring=%d", r.ID)
			if r.NextDue <= today {
				out = append(out, Event{Key: fmt.Sprintf("recurring-due:%d:%s", r.ID, r.NextDue), Link: link,
					Text: i18n.Tf(lang, "Wiederkehrende Rechnung „%s“ für %s ist fällig (%s).", r.Name, r.ContactName, date(r.NextDue))})
			} else {
				out = append(out, Event{Key: fmt.Sprintf("recurring-soon:%d:%s", r.ID, r.NextDue), Link: link,
					Text: i18n.Tf(lang, "Wiederkehrende Rechnung „%s“ für %s ist am %s fällig.", r.Name, r.ContactName, date(r.NextDue))})
			}
		}
	}

	if settings.NotifyOverdue {
		open, err := n.Store.OpenInvoices(ctx)
		if err != nil {
			return nil, err
		}
		for _, inv := range open {
			if inv.IsPaid() || inv.DueDate == "" || inv.DueDate >= today {
				continue
			}
			out = append(out, Event{Key: fmt.Sprintf("invoice-overdue:%d:%s", inv.ID, inv.DueDate), Link: fmt.Sprintf("/invoices/%d", inv.ID),
				Text: i18n.Tf(lang, "Rechnung %s an %s ist seit %s überfällig, offen %s.", inv.Number, inv.RecipientName, date(inv.DueDate), eur(inv.OpenCents()))})
		}
	}

	if settings.NotifyBackup {
		last, err := n.Store.LastBackup(ctx)
		if err != nil {
			return nil, err
		}
		fresh, err := n.Store.Fresh(ctx)
		if err != nil {
			return nil, err
		}
		if !fresh && (last.IsZero() || now.Sub(last) > backupStaleAfter) {
			// Once a week at most: the key changes with the week.
			y, w := now.ISOWeek()
			text := i18n.T(lang, "Es gibt noch keine Datensicherung.")
			if !last.IsZero() {
				text = i18n.Tf(lang, "Die letzte Datensicherung ist %d Tage alt.", int(now.Sub(last).Hours()/24))
			}
			out = append(out, Event{Key: fmt.Sprintf("backup-stale:%d-W%02d", y, w), Link: "/settings#backup", Text: text})
		}
	}

	if settings.NotifyQueues {
		for _, q := range []struct {
			queue store.Queue
			text  string
			link  string
		}{
			{store.ReceiptQueue, "%d Beleg(e) warten auf die Prüfung.", "/receipts/review"},
			{store.InvoiceQueue, "%d importierte Rechnung(en) warten auf die Prüfung.", "/invoices/import/review"},
		} {
			items, err := n.Store.ImportQueue(ctx, q.queue)
			if err != nil {
				return nil, err
			}
			// Only once a day has passed: a list being worked on right now
			// is no news. The oldest upload names the batch.
			if len(items) == 0 || now.Sub(items[0].CreatedAt) < 24*time.Hour {
				continue
			}
			out = append(out, Event{Key: fmt.Sprintf("queue:%s:%d", q.queue, items[0].ID), Link: q.link,
				Text: i18n.Tf(lang, q.text, len(items))})
		}
	}
	return out, nil
}

// Compose writes the mail for events.
func (n *Notifier) Compose(settings store.Settings, events []Event) (string, string) {
	lang := settings.Language
	subject := i18n.Tf(lang, "kmu-buchhaltung: %d Erinnerung(en)", len(events))
	if len(events) == 1 {
		subject = "kmu-buchhaltung: " + events[0].Text
	}
	var b strings.Builder
	for _, e := range events {
		b.WriteString("- " + e.Text + "\n")
		if n.PublicURL != "" && e.Link != "" {
			b.WriteString("  " + strings.TrimRight(n.PublicURL, "/") + e.Link + "\n")
		}
	}
	b.WriteString("\n-- \n" + i18n.T(lang, "Diese Nachricht kommt von Ihrer kmu-buchhaltung. Welche Erinnerungen per E-Mail kommen, stellen Sie unter Einstellungen → Benachrichtigungen ein.") + "\n")
	return subject, b.String()
}

func (n *Notifier) mailer(ctx context.Context) (Mailer, error) {
	if n.Mailer != nil {
		return n.Mailer, nil
	}
	if n.Resolve == nil {
		return nil, nil
	}
	return n.Resolve(ctx)
}

func (n *Notifier) clock() time.Time {
	if n.now != nil {
		return n.now()
	}
	return time.Now()
}

func (n *Notifier) logf(format string, args ...any) {
	if n.Log != nil {
		n.Log.Printf(format, args...)
	}
}
