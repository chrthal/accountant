package server_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/server"
	"github.com/chrthal/accountant/internal/store"
)

// newTestServer runs against SQLite: these tests exercise HTTP behaviour, and
// the engine-portability question is covered in the store's own tests.
func newTestServer(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	dsn := "sqlite://" + filepath.Join(t.TempDir(), "test.db")
	if v := os.Getenv("TEST_SERVER_DSN"); v != "" {
		dsn = v
	}
	st, err := store.Open(dsn)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return server.New(st, "test"), st
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func post(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	h, _ := newTestServer(t)
	rec := get(t, h, "/healthz")
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("healthz = %d %q, want 200 \"ok\"", rec.Code, rec.Body.String())
	}
}

func TestPagesRender(t *testing.T) {
	h, _ := newTestServer(t)
	for _, path := range []string{"/", "/entries/new", "/entries/new?direction=income",
		"/contacts", "/categories"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
			continue
		}
		body := rec.Body.String()
		if !strings.Contains(body, "kmu-buchhaltung") {
			t.Errorf("GET %s: layout did not render", path)
		}
		// Phone-first: every page must carry the viewport meta, or the mobile
		// layout silently degrades to a zoomed-out desktop page (ADR 0004).
		if !strings.Contains(body, `name="viewport"`) {
			t.Errorf("GET %s: missing viewport meta", path)
		}
	}
}

// The Phase 1 acceptance criterion, driven the way a person would: create a
// contact, then an outgoing and an income entry, through the forms.
func TestCreateContactAndEntriesThroughTheForms(t *testing.T) {
	h, st := newTestServer(t)

	rec := post(t, h, "/contacts", url.Values{
		"kind": {"customer"}, "name": {"Muster GmbH"}, "uid": {"ATU12345678"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /contacts = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	contacts, err := st.ListContacts(t.Context())
	if err != nil || len(contacts) != 1 {
		t.Fatalf("contacts = %v (err %v), want exactly one", contacts, err)
	}

	for _, e := range []struct{ direction, amount, rate, desc string }{
		{"outgoing", "1.234,56", "20", "Büromaterial"},
		{"income", "2.000,00", "20", "Beratung März"},
	} {
		rec := post(t, h, "/entries", url.Values{
			"booked_on": {"2026-03-15"}, "direction": {e.direction},
			"description": {e.desc}, "amount": {e.amount}, "vat_rate": {e.rate},
			"konto": {"4000"}, "gegenkonto": {"2800"},
		})
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST /entries (%s) = %d, want 303: %s", e.direction, rec.Code, rec.Body.String())
		}
	}

	entries, err := st.ListEntries(t.Context(), 10)
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	for _, e := range entries {
		if e.Konto == "" || e.Gegenkonto == "" {
			t.Errorf("entry #%d has no Konto/Gegenkonto — the DATEV export needs both", e.ID)
		}
	}

	// The typed "1.234,56" must survive as exact cents.
	body := get(t, h, "/").Body.String()
	for _, want := range []string{"1.234,56", "2.000,00", "Büromaterial", "Beratung März"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard does not show %q", want)
		}
	}
}

func TestEntryFormRejectsBadInputWithoutLosingIt(t *testing.T) {
	h, _ := newTestServer(t)

	form := url.Values{
		"booked_on": {"2026-03-15"}, "direction": {"outgoing"},
		"description": {"Kaffee"}, "amount": {"nicht-zahl"}, "vat_rate": {"20"},
		"konto": {"7600"}, "gegenkonto": {"2800"},
	}
	rec := post(t, h, "/entries", form)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST bad amount = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	// Re-rendering the form must hand back what was typed; retyping a booking
	// because one field was wrong is exactly the annoyance to avoid.
	if !strings.Contains(body, "Kaffee") {
		t.Error("form lost the description on re-render")
	}
	if !strings.Contains(body, "Betrag") {
		t.Error("no error message about the amount")
	}

	// A missing Konto is *not* an error any more (ADR 0012): the booking goes
	// through, and only the DATEV export cares.
	form.Set("amount", "10,00")
	form.Set("konto", "")
	form.Set("gegenkonto", "")
	if rec := post(t, h, "/entries", form); rec.Code != http.StatusSeeOther {
		t.Errorf("POST without Konto = %d, want 303 — accounts are optional", rec.Code)
	}
}

func TestReverseThroughTheUI(t *testing.T) {
	h, st := newTestServer(t)

	if rec := post(t, h, "/entries", url.Values{
		"booked_on": {"2026-03-15"}, "direction": {"outgoing"},
		"description": {"Fehlbuchung"}, "amount": {"50,00"}, "vat_rate": {"20"},
		"konto": {"7600"}, "gegenkonto": {"2800"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("create = %d, want 303", rec.Code)
	}

	entries, _ := st.ListEntries(t.Context(), 10)
	id := entries[0].ID

	rec := post(t, h, "/entries/"+strconv.FormatInt(id, 10)+"/reverse", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("reverse = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	after, _ := st.ListEntries(t.Context(), 10)
	if len(after) != 2 {
		t.Fatalf("got %d entries after reversal, want 2 (the original is kept)", len(after))
	}

	// Reversing twice must be refused rather than double-counted.
	if rec := post(t, h, "/entries/"+strconv.FormatInt(id, 10)+"/reverse", nil); rec.Code != http.StatusConflict {
		t.Errorf("second reverse = %d, want 409", rec.Code)
	}
	if rec := post(t, h, "/entries/999999/reverse", nil); rec.Code != http.StatusNotFound {
		t.Errorf("reverse of a missing entry = %d, want 404", rec.Code)
	}

	// Both rows stay visible on the dashboard, marked as no longer counting.
	body := get(t, h, "/").Body.String()
	if !strings.Contains(body, "Storno") || !strings.Contains(body, "storniert durch") {
		t.Error("dashboard does not show the reversal relationship")
	}
}

// The whole point of ADR 0012: account numbers are configured once on a
// category by whoever knows the chart of accounts, and a booking picks them up
// without the user typing them.
func TestCategoryDefaultsReachTheBookingThroughTheUI(t *testing.T) {
	h, st := newTestServer(t)
	enableDATEV(t, h)

	if rec := post(t, h, "/categories", url.Values{
		"name": {"Bürobedarf"}, "direction": {"outgoing"},
		"default_konto": {"7600"}, "default_gegenkonto": {"2700"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("create category = %d, want 303", rec.Code)
	}

	cats, err := st.ListCategories(t.Context())
	if err != nil || len(cats) != 1 {
		t.Fatalf("categories = %v, %v", cats, err)
	}

	// The booking form offers it, with its accounts visible.
	body := get(t, h, "/entries/new").Body.String()
	if !strings.Contains(body, "Bürobedarf") || !strings.Contains(body, "7600") {
		t.Error("the booking form does not offer the category and its accounts")
	}

	// Book with a category and no accounts typed at all.
	if rec := post(t, h, "/entries", url.Values{
		"booked_on": {"2026-03-15"}, "direction": {"outgoing"},
		"description": {"Druckerpapier"}, "amount": {"24,90"}, "vat_rate": {"20"},
		"category_id": {strconv.FormatInt(cats[0].ID, 10)},
		"konto":       {""}, "gegenkonto": {""},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("create booking = %d, want 303", rec.Code)
	}

	entries, _ := st.ListEntries(t.Context(), 10)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Konto != "7600" || entries[0].Gegenkonto != "2700" {
		t.Errorf("booked accounts = %q/%q, want 7600/2700",
			entries[0].Konto, entries[0].Gegenkonto)
	}
}

// A booking with no accounts blocks the DATEV export and says which one — but
// leaves everything else, including the CSV export, working.
func TestReportsPageNamesBookingsWithoutAccounts(t *testing.T) {
	h, _ := newTestServer(t)
	enableDATEV(t, h)

	if rec := post(t, h, "/entries", url.Values{
		"booked_on": {"2026-03-15"}, "direction": {"income"},
		"description": {"Beratung"}, "amount": {"1.200,00"}, "vat_rate": {"20"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("create booking = %d, want 303", rec.Code)
	}

	body := get(t, h, "/reports?year=2026").Body.String()
	if !strings.Contains(body, "ohne") || !strings.Contains(body, "#1") {
		t.Error("the reports page does not name the booking without accounts")
	}
	if strings.Contains(body, "/export/datev") {
		t.Error("the DATEV export is still offered despite a booking without accounts")
	}

	// The CSV export does not care — it is not a double-entry format.
	if rec := get(t, h, "/export/csv?year=2026"); rec.Code != http.StatusOK {
		t.Errorf("CSV export = %d, want 200", rec.Code)
	}
}
