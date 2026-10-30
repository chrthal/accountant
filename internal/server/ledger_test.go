package server_test

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/store"
)

func book(t *testing.T, h http.Handler, date, dir, desc, amount string, extra ...string) {
	t.Helper()
	form := url.Values{
		"booked_on": {date}, "direction": {dir}, "description": {desc},
		"amount": {amount}, "vat_rate": {"20"},
	}
	for i := 0; i+1 < len(extra); i += 2 {
		form.Set(extra[i], extra[i+1])
	}
	if rec := post(t, h, "/entries", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("book %q = %d: %s", desc, rec.Code, rec.Body.String())
	}
}

func TestNewPagesRenderInTheShell(t *testing.T) {
	h, st := newTestServer(t)
	configureIssuer(t, h, false)
	contactID := createContact(t, h, st)
	book(t, h, "2026-03-15", "outgoing", "Papier", "10,00")
	cat, _ := st.CreateCategory(t.Context(), store.Category{Name: "Büro", Direction: "outgoing"})
	post(t, h, "/invoices", invoiceForm(contactID, [5]string{"Beratung", "1", "Std.", "100,00", "20"}))

	for path, nav := range map[string]string{
		"/":           `href="/" aria-current="page"`,
		"/entries":    `href="/entries" aria-current="page"`,
		"/entries/1":  `href="/entries" aria-current="page"`,
		"/invoices/1": `href="/invoices" aria-current="page"`,
		"/contacts/" + strconv.FormatInt(contactID, 10): `href="/contacts" aria-current="page"`,
		"/categories/" + strconv.FormatInt(cat, 10):     `href="/categories" aria-current="page"`,
		"/invoices/new?from=1":                          `href="/invoices" aria-current="page"`,
	} {
		rec := get(t, h, path)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d", path, rec.Code)
			continue
		}
		body := rec.Body.String()
		if !strings.Contains(body, nav) {
			t.Errorf("GET %s does not mark its navigation item", path)
		}
		// No inline scripts: the CSP forbids them (ADR 0013, ADR 0017).
		if strings.Contains(body, "<script>") {
			t.Errorf("GET %s carries an inline script", path)
		}
		if strings.Contains(body, " style=") {
			t.Errorf("GET %s carries an inline style attribute", path)
		}
	}
	for _, path := range []string{"/entries/999", "/invoices/999", "/contacts/999", "/categories/999"} {
		if rec := get(t, h, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}

func TestLedgerFiltersThroughTheUI(t *testing.T) {
	h, _ := newTestServer(t)
	book(t, h, "2026-02-03", "outgoing", "Zugticket Wien", "50,00")
	book(t, h, "2026-03-04", "outgoing", "Hotel Graz", "200,00")
	book(t, h, "2026-03-05", "income", "Honorar März", "1.200,00")

	body := get(t, h, "/entries?year=2026&q=zug").Body.String()
	if !strings.Contains(body, "Zugticket Wien") || strings.Contains(body, "Hotel Graz") {
		t.Error("text filter does not narrow the list")
	}
	body = get(t, h, "/entries?year=2026&month=3&direction=outgoing").Body.String()
	if !strings.Contains(body, "Hotel Graz") || strings.Contains(body, "Honorar März") || strings.Contains(body, "Zugticket") {
		t.Error("month + direction filter does not narrow the list")
	}
	if !strings.Contains(body, "200,00") {
		t.Error("the filtered sum is not shown")
	}
	body = get(t, h, "/entries?year=2025").Body.String()
	if strings.Contains(body, "Hotel Graz") {
		t.Error("the year filter lets other years through")
	}
}

func TestEntryDetailShowsTheStornoPairAndRedirects(t *testing.T) {
	h, _ := newTestServer(t)
	rec := post(t, h, "/entries", url.Values{
		"booked_on": {"2026-03-15"}, "direction": {"outgoing"}, "description": {"Fehlbuchung"},
		"amount": {"50,00"}, "vat_rate": {"20"},
	})
	if loc := rec.Header().Get("Location"); loc != "/entries/1" {
		t.Errorf("after booking, redirect to %q, want the new booking", loc)
	}
	body := get(t, h, "/entries/1").Body.String()
	if !strings.Contains(body, "/entries/1/reverse") || !strings.Contains(body, "data-confirm") {
		t.Error("an effective booking offers no (confirmed) Storno")
	}
	post(t, h, "/entries/1/reverse", nil)
	body = get(t, h, "/entries/1").Body.String()
	if strings.Contains(body, "/entries/1/reverse") {
		t.Error("a reversed booking still offers a Storno")
	}
	if !strings.Contains(body, `href="/entries/2"`) {
		t.Error("the reversed booking does not link to its Storno")
	}
}

func TestEditContactAndCategory(t *testing.T) {
	h, st := newTestServer(t)
	id := createContact(t, h, st)
	rec := post(t, h, "/contacts/"+strconv.FormatInt(id, 10), url.Values{
		"kind": {"both"}, "name": {"Muster AG"}, "uid": {"ATU99999999"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("update contact = %d: %s", rec.Code, rec.Body.String())
	}
	if c, _ := st.GetContact(t.Context(), id); c.Name != "Muster AG" || c.UID != "ATU99999999" {
		t.Errorf("contact = %+v", c)
	}
	if rec := post(t, h, "/contacts/"+strconv.FormatInt(id, 10), url.Values{"kind": {"both"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("contact without name = %d, want 422", rec.Code)
	}

	cat, _ := st.CreateCategory(t.Context(), store.Category{Name: "Büro", Direction: "outgoing"})
	rec = post(t, h, "/categories/"+strconv.FormatInt(cat, 10), url.Values{
		"name": {"Bürobedarf"}, "direction": {"outgoing"}, "default_konto": {"7600"}, "default_gegenkonto": {"2700"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("update category = %d: %s", rec.Code, rec.Body.String())
	}
	if c, _ := st.GetCategory(t.Context(), cat); c.Name != "Bürobedarf" || c.DefaultKonto != "7600" {
		t.Errorf("category = %+v", c)
	}
}

// The Phase 8 acceptance criterion through the browser.
func TestInvoiceLifecycleThroughTheUI(t *testing.T) {
	h, st := newTestServer(t)
	if rec := post(t, h, "/settings", url.Values{
		"issuer_name": {"Mustermann IT"}, "issuer_uid": {"ATU12345678"},
		"issuer_iban": {"AT61 1904 3002 3457 3201"}, "payment_terms_days": {"14"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("settings = %d: %s", rec.Code, rec.Body.String())
	}
	contactID := createContact(t, h, st)

	// Seven positions, more than the old fixed five; index 3 is left out, as
	// happens when the browser adds rows after an empty one.
	form := invoiceForm(contactID)
	form.Set("due_date", "2026-09-01")
	for _, i := range []int{0, 1, 2, 4, 5, 6, 7} {
		idx := strconv.Itoa(i)
		rate := "20"
		if i%2 == 1 {
			rate = "10"
		}
		form.Set("description"+idx, "Position "+idx)
		form.Set("quantity"+idx, "1")
		form.Set("unit_price"+idx, "10,00")
		form.Set("vat_rate"+idx, rate)
	}
	rec := post(t, h, "/invoices", form)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/invoices/1" {
		t.Fatalf("issue = %d → %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	inv, _ := st.GetInvoice(t.Context(), 1)
	if len(inv.Lines) != 7 {
		t.Fatalf("lines = %d, want 7", len(inv.Lines))
	}

	// Due 2026-09-01, unpaid: overdue from today (2026-09-29) on.
	body := get(t, h, "/invoices/1").Body.String()
	if !strings.Contains(body, "überfällig") || !strings.Contains(body, "Zahlung buchen") {
		t.Error("the invoice is not shown as overdue with a payment action")
	}

	rec = post(t, h, "/invoices/1/payment", url.Values{"paid_on": {"2026-09-28"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("payment = %d: %s", rec.Code, rec.Body.String())
	}
	body = get(t, h, "/invoices/1").Body.String()
	if !strings.Contains(body, "bezahlt") || strings.Contains(body, "Zahlung buchen</button>") {
		t.Error("the paid invoice is not shown as paid")
	}
	if rec := post(t, h, "/invoices/1/payment", url.Values{"paid_on": {"2026-09-28"}}); rec.Code != http.StatusConflict {
		t.Errorf("second payment = %d, want 409", rec.Code)
	}

	entries, _ := st.EntriesForInvoice(t.Context(), 1)
	var sum int64
	for _, e := range entries {
		sum += e.AmountGrossCents
	}
	if len(entries) != 2 || sum != inv.GrossCents {
		t.Errorf("payment entries = %d summing %d, want 2 summing %d", len(entries), sum, inv.GrossCents)
	}

	// Storno of one payment entry puts the invoice back to open.
	post(t, h, "/entries/"+strconv.FormatInt(entries[0].ID, 10)+"/reverse", nil)
	if again, _ := st.GetInvoice(t.Context(), 1); again.IsPaid() {
		t.Error("the invoice stays paid after a payment Storno")
	}

	// "Als Vorlage": the positions come back, as does the recipient.
	body = get(t, h, "/invoices/new?from=1").Body.String()
	if !strings.Contains(body, `value="Position 7"`) || !strings.Contains(body, "selected>Muster GmbH") {
		t.Error("the template does not carry positions and recipient")
	}
	// The browser-side row template is there, with placeholder markers.
	if !strings.Contains(body, `name="description__i__"`) {
		t.Error("no <template> row for adding positions")
	}
}

func TestDateFormatSetting(t *testing.T) {
	h, st := newTestServer(t)
	book(t, h, "2026-03-15", "outgoing", "Papier", "10,00")
	if body := get(t, h, "/entries?year=2026").Body.String(); !strings.Contains(body, "15.03.2026") {
		t.Error("the default is not the Austrian format")
	}
	if rec := post(t, h, "/settings", url.Values{"issuer_name": {"X"}, "date_format": {"2006-01-02"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body.String())
	}
	body := get(t, h, "/entries?year=2026").Body.String()
	if !strings.Contains(body, `<span class="sub">2026-03-15`) || strings.Contains(body, "15.03.2026") {
		t.Error("the chosen ISO format is not applied")
	}
	if !strings.Contains(get(t, h, "/settings").Body.String(), `value="2006-01-02" selected`) {
		t.Error("the settings page does not show the chosen format")
	}
	if rec := post(t, h, "/settings", url.Values{"issuer_name": {"X"}, "date_format": {"Jan 2 2006"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("an unknown layout was accepted (%d)", rec.Code)
	}
	if s, _ := st.Settings(t.Context()); s.DateFormat != "2006-01-02" {
		t.Errorf("stored format = %q", s.DateFormat)
	}
}
