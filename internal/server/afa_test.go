package server_test

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// ADR 0037 through the pages: a 50 % category, an asset, the profit, the
// Anlagenverzeichnis and the CSV.
func TestBewirtungAndAfAThroughTheUI(t *testing.T) {
	h, st := newTestServer(t)
	configureIssuer(t, h, false)
	if rec := post(t, h, "/categories", url.Values{"name": {"Bewirtung"}, "direction": {"outgoing"}, "deductible_pct": {"50"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("create category = %d", rec.Code)
	}
	if body := get(t, h, "/categories").Body.String(); !strings.Contains(body, "50 % absetzbar") {
		t.Error("the category list does not show the share")
	}
	if edit := get(t, h, "/categories/1").Body.String(); !strings.Contains(edit, `<option value="50" selected>`) {
		t.Error("the edit form does not keep the share")
	}
	book(t, h, "2026-02-01", "income", "Beratung", "6.000,00")
	book(t, h, "2026-02-10", "outgoing", "Geschäftsessen", "110,00", "vat_rate", "10", "category_id", "1")
	book(t, h, "2026-03-10", "outgoing", "Laptop", "1.800,00")

	entry := get(t, h, "/entries/3").Body.String()
	if !strings.Contains(entry, `action="/entries/3/asset"`) || !strings.Contains(entry, "über 1.000 €") {
		t.Fatal("the booking page does not offer the asset, or misses the GWG hint")
	}
	if rec := post(t, h, "/entries/3/asset", url.Values{"name": {"Laptop"}, "years": {"3"}, "in_use_from": {"10.03.2026"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("create asset = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := post(t, h, "/entries/1/asset", url.Values{"years": {"3"}, "in_use_from": {"01.02.2026"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("income as an asset = %d, want 422", rec.Code)
	}

	reports := get(t, h, "/reports?year=2026").Body.String()
	// 5.000,00 net income − 50,00 (half the meal) − 500,00 AfA = 4.450,00
	for _, want := range []string{"Gewinn", "4.450,00", "Abschreibung (AfA)", "500,00", "nicht absetzbar"} {
		if !strings.Contains(reports, want) {
			t.Errorf("reports lack %q", want)
		}
	}
	assets := get(t, h, "/assets?year=2027").Body.String()
	if !strings.Contains(assets, "Laptop") || !strings.Contains(assets, "1.500,00") || !strings.Contains(assets, "500,00") {
		t.Error("the Anlagenverzeichnis 2027 is incomplete")
	}
	csv := get(t, h, "/export/csv?year=2026").Body.String()
	for _, want := range []string{"Anlagenverzeichnis", "Anlage, AfA siehe Anlagenverzeichnis", "50 % absetzbar", ";Laptop;2026-03-10;2026-03-10;3;1.500,00;500,00;1.000,00"} {
		if !strings.Contains(csv, want) {
			t.Errorf("CSV lacks %q", want)
		}
	}

	a, ok, _ := st.AssetForEntry(t.Context(), 3)
	if !ok {
		t.Fatal("no asset stored")
	}
	post(t, h, "/assets/"+itoa64(a.ID)+"/delete", url.Values{"entry_id": {"3"}})
	if reports := get(t, h, "/reports?year=2026").Body.String(); !strings.Contains(reports, "3.450,00") {
		t.Error("after removing the asset, the laptop must count in full: 5.000 − 50 − 1.500 = 3.450")
	}
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
