package server_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func bookIncome(t *testing.T, h http.Handler, date, amount, desc string) {
	t.Helper()
	if rec := post(t, h, "/entries", url.Values{
		"booked_on": {date}, "direction": {"income"}, "description": {desc},
		"amount": {amount}, "vat_rate": {"0"}, "konto": {"2000"}, "gegenkonto": {"4000"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("book %s: %d", desc, rec.Code)
	}
}

func TestThresholdTrackerBands(t *testing.T) {
	h, _ := newTestServer(t)

	// Under the limit.
	bookIncome(t, h, "2026-01-15", "40.000,00", "Projekt A")
	body := get(t, h, "/reports?year=2026").Body.String()
	if !strings.Contains(body, "40.000,00") {
		t.Error("revenue not shown")
	}
	if !strings.Contains(body, "bis zur Grenze") {
		t.Error("remaining-to-threshold message missing while under the limit")
	}

	// Into the 10 % tolerance band.
	bookIncome(t, h, "2026-06-01", "17.000,00", "Projekt B") // 57.000
	body = get(t, h, "/reports?year=2026").Body.String()
	if !strings.Contains(body, "Toleranz") || !strings.Contains(body, "bis Jahresende") {
		t.Errorf("tolerance band not explained:\n%s", body)
	}

	// Past the ceiling: the booking that crossed it must be named, since the
	// retroactive consequence attaches to it.
	bookIncome(t, h, "2026-09-01", "5.000,00", "Projekt C") // 62.000
	body = get(t, h, "/reports?year=2026").Body.String()
	if !strings.Contains(body, "Toleranzgrenze von 60.500") {
		t.Error("exceeded band not reported")
	}
	if !strings.Contains(body, "Projekt C") {
		t.Error("the booking that crossed the ceiling is not named")
	}
	if !strings.Contains(body, "rückwirkend") {
		t.Error("the retroactive consequence is not mentioned")
	}

	// And the caveat must sit next to the number, not in a document.
	if !strings.Contains(body, "keine Steuerberatung") {
		t.Error("the 'not tax advice' caveat is missing from the tracker")
	}
}

// A reversed booking must not count towards the limit — nor may its Storno.
func TestThresholdIgnoresReversedBookings(t *testing.T) {
	h, st := newTestServer(t)
	bookIncome(t, h, "2026-02-01", "50.000,00", "Echt")
	bookIncome(t, h, "2026-02-02", "9.000,00", "Irrtum")

	entries, _ := st.ListEntries(t.Context(), 10)
	var wrongID int64
	for _, e := range entries {
		if e.Description == "Irrtum" {
			wrongID = e.ID
		}
	}
	if _, err := st.ReverseEntry(t.Context(), wrongID, "2026-02-03"); err != nil {
		t.Fatalf("reverse: %v", err)
	}

	th, err := st.Threshold(t.Context(), 2026)
	if err != nil {
		t.Fatalf("threshold: %v", err)
	}
	if th.RevenueCents != 5_000_000 {
		t.Errorf("revenue = %d, want 5000000 — the reversed pair must not count", th.RevenueCents)
	}
	if th.Band != "under" {
		t.Errorf("band = %q, want under", th.Band)
	}
}

func TestCSVExportDownload(t *testing.T) {
	h, _ := newTestServer(t)
	bookIncome(t, h, "2026-03-01", "1.234,56", "Honorar März")

	rec := get(t, h, "/export/csv?year=2026")
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "Buchungen-2026.csv") {
		t.Errorf("Content-Disposition = %q", rec.Header().Get("Content-Disposition"))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Honorar März") || !strings.Contains(body, "1.234,56") {
		t.Errorf("export does not contain the booking:\n%s", body)
	}
	if !strings.Contains(body, "USt-Zusammenfassung") {
		t.Error("VAT summary missing from the export")
	}
}

// Without the Steuerberater's numbers the DATEV export must refuse, and say so
// where the user can act on it (ADR 0010).
func TestDATEVExportRefusesWithoutConfiguration(t *testing.T) {
	h, _ := newTestServer(t)
	enableDATEV(t, h)
	bookIncome(t, h, "2026-03-01", "100,00", "Honorar")

	rec := get(t, h, "/export/datev?year=2026")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("DATEV export without config = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Berater-Nummer") {
		t.Errorf("refusal does not say what is missing: %s", rec.Body.String())
	}

	reports := get(t, h, "/reports?year=2026").Body.String()
	if !strings.Contains(reports, "noch nicht möglich") {
		t.Error("the reports page does not warn that DATEV is unconfigured")
	}
	if strings.Contains(reports, `href="/export/datev`) {
		t.Error("the DATEV link is offered even though it cannot work")
	}
}

func TestDATEVExportWithConfiguration(t *testing.T) {
	h, _ := newTestServer(t)
	if rec := post(t, h, "/settings", url.Values{
		"issuer_name":             {"Mustermann IT"},
		"datev_enabled":           {"1"},
		"datev_consultant_number": {"1234567"},
		"datev_client_number":     {"78901"},
		"datev_account_length":    {"4"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("settings = %d", rec.Code)
	}
	bookIncome(t, h, "2026-03-15", "1.200,00", "Honorar März")

	rec := get(t, h, "/export/datev?year=2026")
	if rec.Code != http.StatusOK {
		t.Fatalf("DATEV export = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "windows-1252") {
		t.Errorf("Content-Type = %q, want cp1252 — DATEV does not read UTF-8", ct)
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, `"EXTF";700;21;"Buchungsstapel"`) {
		t.Errorf("not an EXTF file: %.80s", body)
	}
	if !strings.Contains(body, "1234567;78901") {
		t.Error("Berater-/Mandanten-Nummer missing from the header")
	}
	if !strings.Contains(body, "1503") {
		t.Error("Belegdatum not written as DDMM")
	}

	// The page must keep saying the format is unvalidated — that is the honest
	// state until someone runs DATEV's Prüfprogramm on it (ADR 0010).
	reports := get(t, h, "/reports?year=2026").Body.String()
	if !strings.Contains(reports, "Format-Prüfprogramm") {
		t.Error("the unvalidated-format caveat is missing from the reports page")
	}
}

func TestSVSLimitsAreShown(t *testing.T) {
	h, _ := newTestServer(t)
	post(t, h, "/settings", url.Values{"issuer_name": {"X"}, "kleinunternehmer": {"1"}, "svs_exemption": {"1"}})
	post(t, h, "/entries", url.Values{"booked_on": {"2026-03-15"}, "direction": {"income"},
		"description": {"Beratung"}, "amount": {"8.000,00"}, "vat_rate": {"0"}})
	post(t, h, "/entries", url.Values{"booked_on": {"2026-03-16"}, "direction": {"outgoing"},
		"description": {"Laptop"}, "amount": {"1.200,00"}, "vat_rate": {"20"}})

	// Profit: 8.000 − 1.200 gross (exempt period, VAT is cost) = 6.800 > 6.613,20.
	overview := get(t, h, "/?year=2026").Body.String()
	for _, want := range []string{"SVS-Kleinunternehmergrenze 2026", "6.800,00", "6.613,20", "überschritten", "55.000,00"} {
		if !strings.Contains(overview, want) {
			t.Errorf("overview SVS card lacks %q", want)
		}
	}
	if !strings.Contains(get(t, h, "/reports?year=2026").Body.String(), "SVS-Kleinunternehmergrenze 2026") {
		t.Error("the reports page does not show the SVS limits")
	}
	if !strings.Contains(get(t, h, "/reports?year=2031").Body.String(), "keine SVS-Grenzwerte hinterlegt") {
		t.Error("a year without published limits does not say so")
	}

	// Without the setting, the overview does not show the card.
	post(t, h, "/settings", url.Values{"issuer_name": {"X"}, "kleinunternehmer": {"1"}})
	if strings.Contains(get(t, h, "/?year=2026").Body.String(), "SVS-Kleinunternehmergrenze") {
		t.Error("the SVS card shows although the exemption was not applied for")
	}
}

// enableDATEV switches the DATEV export on (ADR 0028); it starts off.
func enableDATEV(t *testing.T, h http.Handler) {
	t.Helper()
	if rec := post(t, h, "/settings", url.Values{"issuer_name": {"Mustermann IT"}, "datev_enabled": {"1"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("enable DATEV = %d", rec.Code)
	}
}

// With DATEV off, the export is refused and its fields are hidden, and an
// account set on a category survives saving it.
func TestDATEVCanBeSwitchedOff(t *testing.T) {
	h, st := newTestServer(t)
	enableDATEV(t, h)
	post(t, h, "/categories", url.Values{"name": {"Büro"}, "direction": {"outgoing"}, "default_konto": {"7600"}, "default_gegenkonto": {"2800"}})
	if !strings.Contains(get(t, h, "/categories").Body.String(), "7600") {
		t.Fatal("with DATEV on, the category accounts are not shown")
	}

	if rec := post(t, h, "/settings", url.Values{"issuer_name": {"Mustermann IT"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("switch off = %d", rec.Code)
	}
	if rec := get(t, h, "/export/datev?year=2026"); rec.Code != http.StatusNotFound {
		t.Errorf("DATEV export while off = %d, want 404", rec.Code)
	}
	for _, path := range []string{"/categories", "/entries/new", "/reports?year=2026", "/categories/1"} {
		body := get(t, h, path).Body.String()
		if strings.Contains(body, `name="default_konto" inputmode`) || strings.Contains(body, "DATEV EXTF") ||
			strings.Contains(body, "Konto/Gegenkonto (nur für den DATEV-Export)") || strings.Contains(body, ">7600<") {
			t.Errorf("%s still shows DATEV while it is off", path)
		}
	}
	if edit := get(t, h, "/categories/1").Body.String(); !strings.Contains(edit, `type="hidden" name="default_konto" value="7600"`) {
		t.Error("the category form does not carry the account along while DATEV is off")
	}
	post(t, h, "/categories/1", url.Values{"name": {"Büromaterial"}, "direction": {"outgoing"}, "default_konto": {"7600"}, "default_gegenkonto": {"2800"}})
	cats, err := st.ListCategories(context.Background())
	if err != nil || len(cats) != 1 || cats[0].DefaultKonto != "7600" || cats[0].Name != "Büromaterial" {
		t.Errorf("category after saving with DATEV off = %+v, %v", cats, err)
	}
}
