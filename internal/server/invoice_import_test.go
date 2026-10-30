package server_test

import (
	"archive/zip"
	"bytes"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var originalInvoice = []byte("%PDF-1.4\n1 0 obj << >> endobj\n% Rechnung E260112001 from another tool\n%%EOF\n")

// ADR 0031: an invoice from another tool is uploaded, confirmed and stored as
// issued, with its original PDF as the document.
func TestImportInvoiceThroughTheUI(t *testing.T) {
	h, st := newTestServer(t)
	configureIssuer(t, h, false)

	if rec := postMultipart(t, h, "/invoices/import", nil, "original", "notes.txt", []byte("not a pdf")); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("a text file = %d, want 422", rec.Code)
	}
	rec := postMultipart(t, h, "/invoices/import", nil, "original", "E260112001_Beispiel.pdf", originalInvoice)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	m := regexp.MustCompile(`name="original" value="([^"]+)"`).FindStringSubmatch(body)
	if m == nil || !strings.Contains(body, `action="/invoices/import/save"`) || !strings.Contains(body, `name="number"`) {
		t.Fatalf("the import form is incomplete:\n%s", body)
	}

	form := url.Values{
		"original": {html.UnescapeString(m[1])}, "original_name": {"E260112001_Beispiel.pdf"},
		"number": {"E260112001"}, "issue_date": {"12.01.2026"}, "service_date": {"12.01.2026"},
		"new_contact_name": {"Beispiel Immobilien GmbH"},
		"description0":     {"Website"}, "quantity0": {"1"}, "unit0": {"Stk."}, "unit_price0": {"1700,00"}, "vat_rate0": {"20"},
	}
	rec = post(t, h, "/invoices/import/save", form)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "imported=1") {
		t.Fatalf("save = %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	page := get(t, h, rec.Header().Get("Location")).Body.String()
	for _, want := range []string{"E260112001", "Beispiel Immobilien GmbH", "importiert", "2.040,00"} {
		if !strings.Contains(page, want) {
			t.Errorf("invoice page lacks %q", want)
		}
	}
	if pdf := get(t, h, "/invoices/1/pdf").Body.Bytes(); !bytes.Equal(pdf, originalInvoice) {
		t.Error("the PDF served is not the original")
	}
	if contacts, _ := st.ListContacts(t.Context()); len(contacts) != 1 || contacts[0].Name != "Beispiel Immobilien GmbH" {
		t.Errorf("contacts = %+v", contacts)
	}

	// The year archive carries the original too.
	arch := get(t, h, "/export/archive?year=2026").Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(arch), int64(len(arch)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "Rechnungen/Rechnung-E260112001.pdf") {
			r, _ := f.Open()
			data, _ := io.ReadAll(r)
			found = bytes.Equal(data, originalInvoice)
		}
	}
	if !found {
		t.Error("the archive does not hold the original PDF")
	}

	// The same number twice is refused, and the form keeps the input.
	form.Set("new_contact_name", "")
	form.Set("contact_id", "1")
	rec = post(t, h, "/invoices/import/save", form)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `value="E260112001"`) {
		t.Errorf("duplicate import = %d", rec.Code)
	}
}
