package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chrthal/accountant/internal/store"
)

func TestNextDateKeepsMonthEnds(t *testing.T) {
	for _, c := range []struct{ from, interval, want string }{
		{"2026-01-31", "monthly", "2026-02-28"},
		{"2026-02-28", "monthly", "2026-03-31"}, // a month end stays one
		{"2026-01-15", "monthly", "2026-02-15"},
		{"2026-11-30", "quarterly", "2027-02-28"},
		{"2026-10-01", "quarterly", "2027-01-01"},
		{"2028-02-29", "yearly", "2029-02-28"},
		{"2026-03-15", "yearly", "2027-03-15"},
	} {
		if got, err := store.NextDate(c.from, c.interval); err != nil || got != c.want {
			t.Errorf("%s + %s = %s (%v), want %s", c.from, c.interval, got, err, c.want)
		}
	}
	if _, err := store.NextDate("2026-01-01", "weekly"); err == nil {
		t.Error("an unknown interval was accepted")
	}
	for _, c := range []struct{ date, interval, want string }{
		{"2026-10-01", "monthly", "10/2026"}, {"2026-11-15", "quarterly", "Q4 2026"}, {"2026-02-01", "quarterly", "Q1 2026"}, {"2026-05-01", "yearly", "2026"},
	} {
		if got := store.PeriodOf(c.date, c.interval); got != c.want {
			t.Errorf("period of %s %s = %q, want %q", c.date, c.interval, got, c.want)
		}
	}
}

// ADR 0038: issuing from a recurring invoice moves its next date on, in the
// same transaction; skipping moves it without an invoice.
func TestRecurringInvoices(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		contactID := setupIssuer(t, st, false)
		rec := store.Recurring{ContactID: contactID, Name: "Hosting", Interval: "monthly", NextDue: "2026-10-01", Active: true,
			Lines: []store.DraftLine{{Description: "Hosting {Zeitraum}", Quantity: "1", UnitPrice: "25,00", VATRate: "20"}}}
		id, err := st.SaveRecurring(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.SaveRecurring(ctx, store.Recurring{ContactID: contactID, Name: "Leer", Interval: "monthly", NextDue: "2026-10-01"}); err == nil {
			t.Error("a recurring invoice without positions was saved")
		}

		due, _ := st.DueRecurring(ctx, time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC), 7)
		if len(due) != 1 {
			t.Errorf("due a week ahead = %d, want 1", len(due))
		}
		if due, _ := st.DueRecurring(ctx, time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC), 7); len(due) != 0 {
			t.Error("due too early")
		}

		inv, err := st.CreateInvoice(ctx, store.NewInvoice{ContactID: contactID, IssueDate: "2026-10-01", ServiceDate: "2026-10-01", RecurringID: id,
			Lines: []store.InvoiceLine{{Description: "Hosting 10/2026", QuantityMilli: 1000, UnitPriceCents: 2500, VATRate: 20}}})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := st.GetRecurring(ctx, id)
		if got.NextDue != "2026-11-01" || got.LastNumber != inv.Number {
			t.Errorf("after issuing: next %s, last %q", got.NextDue, got.LastNumber)
		}

		if err := st.SkipRecurring(ctx, id); err != nil {
			t.Fatal(err)
		}
		if got, _ := st.GetRecurring(ctx, id); got.NextDue != "2026-12-01" {
			t.Errorf("after skipping: next %s", got.NextDue)
		}
		if err := st.SetRecurringActive(ctx, id, false); err != nil {
			t.Fatal(err)
		}
		if due, _ := st.DueRecurring(ctx, time.Date(2026, 12, 5, 0, 0, 0, 0, time.UTC), 7); len(due) != 0 {
			t.Error("a paused recurring invoice is due")
		}

		if err := st.DeleteRecurring(ctx, id); !errors.Is(err, store.ErrRecurringUsed) {
			t.Errorf("deleting a used recurring invoice = %v", err)
		}
		if err := st.DeleteContact(ctx, contactID); err == nil {
			t.Error("a contact with a recurring invoice was deleted")
		}
	})
}
