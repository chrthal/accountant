package server_test

import (
	"context"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every date a page shows follows the date format setting. MM/TT/JJJJ is
// chosen because it differs from both the ISO dates in the database and the
// Austrian default, so a date that skips the setting shows up either way.
var unformattedDate = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2}|\d{1,2}\.\d{1,2}\.\d{2,4})\b`)

func TestPagesFollowTheDateFormat(t *testing.T) {
	h, st := newTestServer(t)
	configureIssuer(t, h, false)
	contactID := createContact(t, h, st)
	post(t, h, "/categories", url.Values{"name": {"Office"}, "direction": {"outgoing"}, "keywords": {"billa"}})
	book(t, h, "2026-03-15", "outgoing", "Paper", "10,00")
	post(t, h, "/entries/1/reverse", nil)
	post(t, h, "/invoices", invoiceForm(contactID, [5]string{"Consulting", "1", "h", "100,00", "20"}))
	post(t, h, "/invoices/1/issue", nil)
	importThroughUI(t, h, "Datum;Betrag;Empfänger;Zweck\n20.08.2026;120,00;Muster GmbH;RE 2026-0001\n03.09.2026;-49,90;BILLA;Card\n")
	if rec := post(t, h, "/settings", url.Values{"issuer_name": {"Mustermann IT"}, "issuer_uid": {"ATU12345678"},
		"date_format": {"01/02/2006"}}); rec.Code != 303 {
		t.Fatalf("set date format = %d: %s", rec.Code, rec.Body.String())
	}

	var findings []string
	for _, path := range []string{"/?year=2026", "/entries?year=2026", "/entries/1", "/entries/2", "/invoices", "/invoices/1",
		"/contacts", "/contacts/1", "/categories", "/bank", "/bank/drafts", "/reports?year=2026",
		"/reports/vat?year=2026", "/reports/vat?year=2026&period=month", "/settings", "/drafts"} {
		rec := get(t, h, path)
		if rec.Code != 200 {
			continue
		}
		text := visibleText(rec.Body.String())
		// The settings page lists every format as an example.
		text = regexp.MustCompile(`\([0-9./-]{10}\)`).ReplaceAllString(text, "")
		for _, m := range unformattedDate.FindAllStringIndex(text, -1) {
			from, to := max(m[0]-50, 0), min(m[1]+20, len(text))
			findings = append(findings, path+": …"+text[from:to]+"…")
		}
	}
	sort.Strings(findings)
	for _, f := range findings {
		t.Error(f)
	}
}

// Date fields are text in the chosen format; ISO still works, and so do
// single-digit days and months.
func TestDateFieldsReadTheChosenFormat(t *testing.T) {
	h, st := newTestServer(t)
	if rec := post(t, h, "/settings", url.Values{"issuer_name": {"Mustermann IT"}, "date_format": {"01/02/2006"}}); rec.Code != 303 {
		t.Fatalf("set date format = %d", rec.Code)
	}
	book(t, h, "03/15/2026", "outgoing", "US style", "10,00")
	book(t, h, "3/5/2026", "outgoing", "Short", "10,00")
	book(t, h, "2026-04-01", "outgoing", "ISO", "10,00")
	if rec := post(t, h, "/entries", url.Values{"booked_on": {"15.03.2026"}, "direction": {"outgoing"},
		"description": {"Wrong format"}, "amount": {"10,00"}, "vat_rate": {"20"}}); rec.Code == 303 {
		t.Error("a date in another format was accepted")
	}
	want := map[string]string{"US style": "2026-03-15", "Short": "2026-03-05", "ISO": "2026-04-01"}
	entries, err := st.ListEntries(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if w, ok := want[e.Description]; ok && e.BookedOn != w {
			t.Errorf("%s: booked on %s, want %s", e.Description, e.BookedOn, w)
		}
	}
	if !strings.Contains(get(t, h, "/entries/new").Body.String(), `placeholder="03/15/2026"`) {
		t.Error("the date field does not show the chosen format")
	}
}
