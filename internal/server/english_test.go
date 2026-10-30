package server_test

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ADR 0024: with English chosen, the pages people use show no German. The
// word list is German that has no business on an English page; legal terms
// that stay German on purpose (Kleinunternehmerregelung, Vorsteuer in
// parentheses, § citations) are not on it.
var germanWords = regexp.MustCompile(`(?i)\b(und|oder|nicht|keine?|Buchungen?|Rechnungen?|Einstellungen|Übersicht|Kategorien?|Kontakte?|Ausgaben?|Einnahmen?|Betrag|speichern|erfassen|bezahlt|offen|zuordnen|Umsätze|Belege?|Zeitraum|Jahr|bitte|wird|werden|für|mit|vom|bis)\b`)

func visibleText(html string) string {
	html = regexp.MustCompile(`(?s)<(script|style|svg)[^>]*>.*?</(script|style|svg)>`).ReplaceAllString(html, " ")
	html = regexp.MustCompile(`(?s)<[^>]*>`).ReplaceAllString(html, " ")
	return strings.Join(strings.Fields(html), " ")
}

func TestEnglishPagesShowNoGerman(t *testing.T) {
	h, st := newTestServer(t)
	configureIssuer(t, h, false)
	contactID := createContact(t, h, st)
	post(t, h, "/categories", url.Values{"name": {"Office"}, "direction": {"outgoing"}, "keywords": {"billa"}})
	book(t, h, "2026-03-15", "outgoing", "Paper", "10,00")
	post(t, h, "/entries/1/reverse", nil)
	post(t, h, "/invoices", invoiceForm(contactID, [5]string{"Consulting", "1", "h", "100,00", "20"}))
	importThroughUI(t, h, "Datum;Betrag;Empfänger;Zweck\n20.08.2026;120,00;Muster GmbH;RE 2026-0001\n03.09.2026;-49,90;BILLA;Card\n")
	if rec := post(t, h, "/settings", url.Values{"issuer_name": {"Mustermann IT"}, "issuer_uid": {"ATU12345678"},
		"language": {"en"}}); rec.Code != 303 {
		t.Fatalf("switch to English = %d: %s", rec.Code, rec.Body.String())
	}

	var findings []string
	for _, path := range []string{"/?year=2026", "/entries?year=2026", "/entries/1", "/entries/new", "/receipts/new",
		"/invoices", "/invoices/1", "/invoices/new", "/contacts", "/contacts/1", "/categories", "/categories/1",
		"/bank", "/bank/drafts", "/reports?year=2026", "/reports/vat?year=2026", "/reports/vat?year=2026&period=month", "/settings", "/help", "/assets?year=2026", "/recurring", "/recurring/new?from=1"} {
		body := get(t, h, path).Body.String()
		if !strings.Contains(body, `lang="en"`) {
			t.Errorf("%s: html lang is not en", path)
		}
		text := visibleText(body)
		// Data the user typed or the bank sent stays as it is.
		for _, data := range []string{"Muster GmbH", "Mustermann IT", "Musterstraße 1 8010 Graz", "Paper", "Consulting", "BILLA", "RE 2026-0001", "Office", "Card", "Beispielgasse 2 1010 Wien"} {
			text = strings.ReplaceAll(text, data, "")
		}
		seen := map[string]bool{}
		for _, loc := range germanWords.FindAllStringIndex(text, -1) {
			from, to := max(loc[0]-40, 0), min(loc[1]+40, len(text))
			snippet := text[from:to]
			if !seen[snippet] {
				seen[snippet] = true
				findings = append(findings, path+": …"+snippet+"…")
			}
		}
	}
	sort.Strings(findings)
	for _, f := range findings {
		t.Error(f)
	}
	// Amounts follow the language.
	if !strings.Contains(get(t, h, "/invoices/1").Body.String(), "120.00") {
		t.Error("amounts are not in English format")
	}
}
