package server_test

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// ADR 0025: a logo is uploaded, shown in the settings, embedded in invoice
// PDFs and removable; the day scheme numbers invoices through the UI.
func TestInvoiceLogoAndDayNumbers(t *testing.T) {
	h, st := newTestServer(t)
	if rec := post(t, h, "/settings", url.Values{"issuer_name": {"Mustermann IT"}, "issuer_address": {"Graz"},
		"issuer_iban": {"AT61 1904 3002 3457 3201"}, "issuer_bic": {"abcdatwwxxx"}, "issuer_contact": {"office@example.at"},
		"invoice_numbering": {"day"}, "invoice_prefix": {"E"}, "kleinunternehmer": {"1"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("settings = %d: %s", rec.Code, rec.Body.String())
	}
	contactID := createContact(t, h, st)
	form := invoiceForm(contactID, [5]string{"Website", "1", "Stk.", "1700,00", "0"})
	form.Set("issue_date", "2026-01-12")
	form.Set("service_date", "2026-01-12")
	if rec := post(t, h, "/invoices", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("create invoice = %d: %s", rec.Code, rec.Body.String())
	}
	if body := get(t, h, "/invoices/1").Body.String(); !strings.Contains(body, "E260112001") {
		t.Error("the invoice does not carry the day number E260112001")
	}

	if rec := postMultipart(t, h, "/settings/logo", nil, "logo", "logo.txt", []byte("not an image")); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("a text file as logo = %d, want 422", rec.Code)
	}
	var logo bytes.Buffer
	png.Encode(&logo, image.NewGray16(image.Rect(0, 0, 40, 10))) // 16-bit: fpdf needs it converted
	if rec := postMultipart(t, h, "/settings/logo", nil, "logo", "logo.png", logo.Bytes()); rec.Code != http.StatusSeeOther {
		t.Fatalf("upload logo = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(get(t, h, "/settings").Body.String(), `src="/settings/logo"`) {
		t.Error("the settings page does not show the logo")
	}
	if rec := get(t, h, "/settings/logo"); rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("GET logo = %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	pdf := get(t, h, "/invoices/1/pdf").Body.Bytes()
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || !bytes.Contains(pdf, []byte("/Subtype /Image")) {
		t.Error("the invoice PDF has no logo")
	}

	if rec := post(t, h, "/settings/logo/delete", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("delete logo = %d", rec.Code)
	}
	if rec := get(t, h, "/settings/logo"); rec.Code != http.StatusNotFound {
		t.Errorf("GET logo after delete = %d, want 404", rec.Code)
	}
	if bytes.Contains(get(t, h, "/invoices/1/pdf").Body.Bytes(), []byte("/Subtype /Image")) {
		t.Error("the PDF still has a logo after it was removed")
	}
}
