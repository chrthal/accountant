package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/chrthal/accountant/internal/store"
)

// ADR 0038: a recurring invoice reminds when due, opens the invoice form for
// its period, and moves on when the invoice is issued, also from a draft.
func TestRecurringInvoicesThroughTheUI(t *testing.T) {
	h, st := newTestServer(t)
	ctx := t.Context()
	configureIssuer(t, h, false)
	contactID := createContact(t, h, st)
	post(t, h, "/invoices", invoiceForm(contactID, [5]string{"Beratung", "1", "Std.", "100,00", "20"}))

	if form := get(t, h, "/recurring/new?from=1").Body.String(); !strings.Contains(form, `value="Beratung"`) || !strings.Contains(form, `selected>Muster GmbH`) {
		t.Fatal("making an invoice recurring does not copy customer and positions")
	}

	due := time.Now().AddDate(0, 0, 3)
	rec := url.Values{"name": {"Hosting"}, "contact_id": {"1"}, "interval": {"monthly"}, "next_due": {due.Format("02.01.2006")},
		"active": {"1"}, "description0": {"Hosting {Zeitraum}"}, "quantity0": {"1"}, "unit0": {""}, "unit_price0": {"25,00"}, "vat_rate0": {"20"}}
	if rec := post(t, h, "/recurring", rec); rec.Code != http.StatusSeeOther {
		t.Fatalf("save recurring = %d: %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/", "/invoices"} {
		if body := get(t, h, path).Body.String(); !strings.Contains(body, "Wiederkehrende Rechnungen fällig") || !strings.Contains(body, "/invoices/new?recurring=1") {
			t.Errorf("%s does not remind of the due recurring invoice", path)
		}
	}
	if body := get(t, h, "/contacts/1").Body.String(); !strings.Contains(body, "Hosting") || !strings.Contains(body, "/recurring/new?contact=1") {
		t.Error("the customer page does not list its recurring invoices")
	}

	form := get(t, h, "/invoices/new?recurring=1").Body.String()
	period := due.Format("01/2006")
	if !strings.Contains(form, `name="recurring_id" value="1"`) || !strings.Contains(form, `value="Hosting `+period+`"`) || strings.Contains(form, "{Zeitraum}") {
		t.Fatalf("the invoice form is not filled for the period %s", period)
	}

	// Saved as a draft first, issued later: the date moves only then.
	inv := invoiceForm(contactID, [5]string{"Hosting " + period, "1", "", "25,00", "20"})
	inv.Set("recurring_id", "1")
	inv.Set("action", "draft")
	post(t, h, "/invoices", inv)
	if r, _ := st.GetRecurring(ctx, 1); r.NextDue != due.Format("2006-01-02") {
		t.Error("saving a draft moved the date")
	}
	drafts, _ := st.ListDrafts(ctx)
	if len(drafts) != 1 || drafts[0].Fields["recurring_id"] != "1" {
		t.Fatalf("the draft lost its recurring invoice: %+v", drafts)
	}
	inv.Set("action", "issue")
	inv.Set("draft_id", "1")
	if rec := post(t, h, "/invoices", inv); rec.Code != http.StatusSeeOther {
		t.Fatalf("issue = %d: %s", rec.Code, rec.Body.String())
	}
	r, _ := st.GetRecurring(ctx, 1)
	next, _ := store.NextDate(due.Format("2006-01-02"), "monthly")
	if r.NextDue != next || r.LastNumber != "2026-0002" {
		t.Errorf("after issuing: next %s (want %s), last %q", r.NextDue, next, r.LastNumber)
	}
	if body := get(t, h, "/contacts/1").Body.String(); !strings.Contains(body, "zuletzt 2026-0002") {
		t.Error("the customer page does not show the last invoice")
	}

	post(t, h, "/recurring/1/skip", url.Values{})
	after, _ := store.NextDate(next, "monthly")
	if r, _ := st.GetRecurring(ctx, 1); r.NextDue != after {
		t.Errorf("after skipping: %s, want %s", r.NextDue, after)
	}
	post(t, h, "/recurring/1/pause", url.Values{})
	if body := get(t, h, "/recurring").Body.String(); !regexp.MustCompile(`pausiert`).MatchString(body) {
		t.Error("the list does not show the pause")
	}
	if rec := post(t, h, "/recurring/1/delete", url.Values{}); rec.Code != http.StatusConflict {
		t.Errorf("deleting a used recurring invoice = %d, want 409", rec.Code)
	}
}

func TestPeriodPlaceholders(t *testing.T) {
	h, st := newTestServer(t)
	configureIssuer(t, h, false)
	createContact(t, h, st)
	post(t, h, "/recurring", url.Values{"name": {"Wartung"}, "contact_id": {"1"}, "interval": {"quarterly"}, "next_due": {"01.10.2026"},
		"active": {"1"}, "description0": {"Maintenance {Period}"}, "quantity0": {"1"}, "unit_price0": {"90,00"}, "vat_rate0": {"20"},
		"note": {"Zeitraum {Zeitraum}"}})
	form := get(t, h, "/invoices/new?recurring=1").Body.String()
	if !strings.Contains(form, `value="Maintenance Q4 2026"`) || !strings.Contains(form, "Zeitraum Q4 2026") {
		t.Error("{Period} or {Zeitraum} is not filled in")
	}
}
