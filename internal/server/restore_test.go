package server_test

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

// ADR 0030: a backup downloaded from one installation is restored into a
// fresh one in the browser, and only into a fresh one.
func TestRestoreInTheBrowser(t *testing.T) {
	src, srcStore := newTestServer(t)
	configureIssuer(t, src, false)
	contactID := createContact(t, src, srcStore)
	post(t, src, "/invoices", invoiceForm(contactID, [5]string{"Beratung", "1", "h", "100,00", "20"}))
	png, err := os.ReadFile("../ocr/testdata/receipt.png")
	if err != nil {
		t.Fatal(err)
	}
	postMultipart(t, src, "/entries", map[string]string{"booked_on": "2026-03-15", "direction": "outgoing",
		"description": "Papier", "amount": "13,00", "vat_rate": "20"}, "receipt", "bon.png", png)
	backup := get(t, src, "/backup").Body.Bytes()

	dst, _ := newTestServer(t)
	if body := get(t, dst, "/").Body.String(); !strings.Contains(body, "/settings#restore") {
		t.Error("a fresh installation does not offer the restore")
	}
	if body := get(t, dst, "/settings").Body.String(); !strings.Contains(body, `action="/restore"`) {
		t.Error("the settings of a fresh installation have no restore form")
	}

	damaged := backup[:len(backup)-50]
	if rec := postMultipart(t, dst, "/restore", nil, "backup", "x.backup.gz", damaged); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "nichts geändert") {
		t.Errorf("damaged backup = %d", rec.Code)
	}

	rec := postMultipart(t, dst, "/restore", nil, "backup", "bookkeeper.backup.gz", backup)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "restored=") {
		t.Fatalf("restore = %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if body := get(t, dst, "/invoices").Body.String(); !strings.Contains(body, "2026-0001") {
		t.Error("the invoice is not back")
	}
	if body := get(t, dst, "/entries/1").Body.String(); !strings.Contains(body, "Papier") || !strings.Contains(body, "bon.png") {
		t.Error("the booking or its receipt is not back")
	}
	if body := get(t, dst, "/settings").Body.String(); !strings.Contains(body, `value="Mustermann IT"`) || strings.Contains(body, `action="/restore"`) {
		t.Error("the settings are not back, or the restore is still offered")
	}

	if rec := postMultipart(t, dst, "/restore", nil, "backup", "bookkeeper.backup.gz", backup); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "neue, leere Installation") {
		t.Errorf("a restore over existing books = %d", rec.Code)
	}
}
