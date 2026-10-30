package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// ADR 0043: the booking page changes the description and shows the history.
func TestRenameEntryPage(t *testing.T) {
	h, _ := newTestServer(t)
	rec := post(t, h, "/entries", url.Values{"booked_on": {"2026-03-01"}, "direction": {"outgoing"},
		"description": {"Rechnung 4711"}, "amount": {"12,00"}, "vat_rate": {"20"}})
	loc := rec.Header().Get("Location")
	if !strings.Contains(get(t, h, loc).Body.String(), "Beschreibung ändern") {
		t.Fatal("no way to change the description")
	}
	if rec := post(t, h, loc+"/description", url.Values{"description": {""}}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty description = %d", rec.Code)
	}
	if rec := post(t, h, loc+"/description", url.Values{"description": {"Druckerpapier"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("rename = %d %s", rec.Code, rec.Body.String())
	}
	page := get(t, h, loc).Body.String()
	for _, want := range []string{"<h1>Druckerpapier</h1>", "gebucht als</span> Rechnung 4711", "geändert in</span> Druckerpapier", "1-mal geändert"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if !strings.Contains(get(t, h, "/entries").Body.String(), "Druckerpapier") {
		t.Error("the list shows the old description")
	}
}
