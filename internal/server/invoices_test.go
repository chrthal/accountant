package server_test

import (
	"bytes"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/store"
)

func configureIssuer(t *testing.T, h http.Handler, ku bool) {
	t.Helper()
	form := url.Values{
		"issuer_name":    {"Mustermann IT"},
		"issuer_address": {"Beispielgasse 2\n1010 Wien"},
		"issuer_uid":     {"ATU12345678"},
	}
	if ku {
		form.Set("kleinunternehmer", "1")
	}
	if rec := post(t, h, "/settings", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings = %d, want 303: %s", rec.Code, rec.Body.String())
	}
}

func createContact(t *testing.T, h http.Handler, st *store.Store) int64 {
	t.Helper()
	if rec := post(t, h, "/contacts", url.Values{
		"kind": {"customer"}, "name": {"Muster GmbH"}, "address": {"Musterstraße 1\n8010 Graz"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /contacts = %d, want 303", rec.Code)
	}
	contacts, err := st.ListContacts(t.Context())
	if err != nil || len(contacts) == 0 {
		t.Fatalf("no contact created: %v", err)
	}
	return contacts[0].ID
}

func invoiceForm(contactID int64, lines ...[5]string) url.Values {
	form := url.Values{
		"contact_id":   {strconv.FormatInt(contactID, 10)},
		"issue_date":   {"2026-08-18"},
		"service_date": {"2026-08-15"},
	}
	for i, l := range lines {
		idx := strconv.Itoa(i)
		form.Set("description"+idx, l[0])
		form.Set("quantity"+idx, l[1])
		form.Set("unit"+idx, l[2])
		form.Set("unit_price"+idx, l[3])
		form.Set("vat_rate"+idx, l[4])
	}
	return form
}

// The Phase 2 acceptance criterion, driven through the browser: a mixed-rate
// invoice, issued and downloaded as a PDF.
func TestIssueMixedRateInvoiceAndDownloadPDF(t *testing.T) {
	h, st := newTestServer(t)
	configureIssuer(t, h, false)
	contactID := createContact(t, h, st)

	rec := post(t, h, "/invoices", invoiceForm(contactID,
		[5]string{"Beratung", "1,5", "Std.", "100,00", "20"},
		[5]string{"Verpflegung", "2", "Stk.", "25,00", "10"},
	))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /invoices = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	invoices, err := st.ListInvoices(t.Context(), 10)
	if err != nil || len(invoices) != 1 {
		t.Fatalf("invoices = %v (err %v), want exactly one", invoices, err)
	}
	inv := invoices[0]
	if inv.Number != "2026-0001" {
		t.Errorf("number = %q, want 2026-0001", inv.Number)
	}
	if inv.NetCents != 20000 || inv.VATCents != 3500 || inv.GrossCents != 23500 {
		t.Errorf("totals = (%d, %d, %d), want (20000, 3500, 23500)",
			inv.NetCents, inv.VATCents, inv.GrossCents)
	}

	// The list page shows it, and the PDF actually downloads.
	if body := get(t, h, "/invoices").Body.String(); !strings.Contains(body, "2026-0001") {
		t.Error("invoice list does not show the new invoice")
	}

	pdf := get(t, h, "/invoices/"+strconv.FormatInt(inv.ID, 10)+"/pdf")
	if pdf.Code != http.StatusOK {
		t.Fatalf("PDF download = %d, want 200", pdf.Code)
	}
	if ct := pdf.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type = %q, want application/pdf", ct)
	}
	if !strings.Contains(pdf.Header().Get("Content-Disposition"), "Rechnung-2026-0001.pdf") {
		t.Errorf("Content-Disposition = %q, want the invoice number in the filename",
			pdf.Header().Get("Content-Disposition"))
	}
	if !bytes.HasPrefix(pdf.Body.Bytes(), []byte("%PDF-")) {
		t.Error("downloaded body is not a PDF")
	}

	if rec := get(t, h, "/invoices/999999/pdf"); rec.Code != http.StatusNotFound {
		t.Errorf("PDF of a missing invoice = %d, want 404", rec.Code)
	}
}

func TestKleinunternehmerInvoiceThroughTheUI(t *testing.T) {
	h, st := newTestServer(t)
	configureIssuer(t, h, true)
	contactID := createContact(t, h, st)

	// The form defaults every line to 0 % when the exemption is active, so
	// submitting it unchanged must produce a valid exempt invoice.
	form := get(t, h, "/invoices/new").Body.String()
	if !strings.Contains(form, "§ 6 Abs 1 Z 27") {
		t.Error("invoice form does not mention the active Kleinunternehmer exemption")
	}

	rec := post(t, h, "/invoices", invoiceForm(contactID,
		[5]string{"Beratung August", "1", "", "1.200,00", "0"},
	))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /invoices = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	invoices, _ := st.ListInvoices(t.Context(), 10)
	inv := invoices[0]
	if inv.VATCents != 0 || inv.GrossCents != inv.NetCents {
		t.Error("Kleinunternehmer invoice carries VAT")
	}
	if !strings.Contains(inv.Note, "§ 6 Abs 1 Z 27") {
		t.Errorf("note = %q, want the exemption citation", inv.Note)
	}

	// Trying to charge VAT anyway is refused rather than quietly corrected.
	rec = post(t, h, "/invoices", invoiceForm(contactID,
		[5]string{"Beratung", "1", "", "100,00", "20"},
	))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("VAT line on an exempt invoice = %d, want 422", rec.Code)
	}
}

func TestInvoiceFormRejectsBadInputWithoutLosingIt(t *testing.T) {
	h, st := newTestServer(t)
	configureIssuer(t, h, false)
	contactID := createContact(t, h, st)

	rec := post(t, h, "/invoices", invoiceForm(contactID,
		[5]string{"Beratung", "eins", "Std.", "100,00", "20"},
	))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad quantity = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Beratung") {
		t.Error("form lost the description on re-render")
	}
	if !strings.Contains(body, "Menge") {
		t.Error("no error message about the quantity")
	}

	// No recipient, and an invoice with no filled lines at all.
	noContact := invoiceForm(0, [5]string{"Beratung", "1", "", "100,00", "20"})
	noContact.Set("contact_id", "")
	if rec := post(t, h, "/invoices", noContact); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing recipient = %d, want 422", rec.Code)
	}
	if rec := post(t, h, "/invoices", invoiceForm(contactID)); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty invoice = %d, want 422", rec.Code)
	}
}

// Without issuer details there is no § 11-compliant invoice to issue, so the
// form has to say so rather than fail at submit time.
func TestInvoiceFormWarnsWhenIssuerIsNotConfigured(t *testing.T) {
	h, _ := newTestServer(t)
	body := get(t, h, "/invoices/new").Body.String()
	if !strings.Contains(body, "Absenderdaten") {
		t.Error("invoice form does not warn about missing issuer settings")
	}
}

func TestSettingsRoundTripThroughTheUI(t *testing.T) {
	h, _ := newTestServer(t)
	configureIssuer(t, h, true)

	body := get(t, h, "/settings").Body.String()
	if !strings.Contains(body, "Mustermann IT") || !strings.Contains(body, "ATU12345678") {
		t.Error("settings page does not show what was saved")
	}
	if !strings.Contains(body, "checked") {
		t.Error("Kleinunternehmer checkbox is not checked after saving it")
	}

	if rec := post(t, h, "/settings", url.Values{"issuer_name": {""}}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty issuer name = %d, want 422", rec.Code)
	}
}
