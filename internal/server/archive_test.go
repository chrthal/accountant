package server_test

import (
	"strconv"
	"time"

	"archive/zip"
	"bytes"
	"github.com/chrthal/accountant/internal/store"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// ADR 0029: the year archive opens without the app and is complete: the
// CSV, every Beleg byte for byte, every issued invoice, and CSV rows that
// name their files in the ZIP.
func TestYearArchive(t *testing.T) {
	h, st := newTestServer(t)
	configureIssuer(t, h, false)
	contactID := createContact(t, h, st)
	png, err := os.ReadFile("../ocr/testdata/receipt.png")
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{"booked_on": "2026-03-15", "direction": "outgoing", "description": "Papier", "amount": "13,00", "vat_rate": "20"}
	if rec := postMultipart(t, h, "/entries", fields, "receipt", "Kassenbon März.png", png); rec.Code != http.StatusSeeOther {
		t.Fatalf("book = %d", rec.Code)
	}
	fields["booked_on"], fields["description"] = "2025-12-30", "Vorjahr"
	postMultipart(t, h, "/entries", fields, "receipt", "alt.png", png)
	post(t, h, "/invoices", invoiceForm(contactID, [5]string{"Beratung", "1", "h", "100,00", "20"}))

	rec := get(t, h, "/export/archive?year=2026")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("archive = %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("not a readable ZIP: %v", err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		r, _ := f.Open()
		files[f.Name], _ = io.ReadAll(r)
		r.Close()
	}
	root := "kmu-buchhaltung-2026/"
	receipt := root + "Belege/2026-03-15_Buchung-1_1_Kassenbon_März.png"
	for _, name := range []string{root + "LIESMICH.txt", root + "Buchungen-2026.csv", receipt, root + "Rechnungen/Rechnung-2026-0001.pdf"} {
		if _, ok := files[name]; !ok {
			t.Errorf("archive lacks %s; has %v", name, keys(files))
		}
	}
	if len(files) != 4 {
		t.Errorf("archive has %d files, want 4 (the 2025 receipt belongs to 2025): %v", len(files), keys(files))
	}
	if !bytes.Equal(files[receipt], png) {
		t.Error("the receipt in the archive differs from the upload")
	}
	if !bytes.HasPrefix(files[root+"Rechnungen/Rechnung-2026-0001.pdf"], []byte("%PDF-")) {
		t.Error("the invoice is not a PDF")
	}
	csv := string(files[root+"Buchungen-2026.csv"])
	if !strings.Contains(csv, ";Belege") || !strings.Contains(csv, "Belege/2026-03-15_Buchung-1_1_Kassenbon_März.png") {
		t.Errorf("the CSV does not name the receipt's path:\n%s", csv)
	}

	// The plain CSV download names the receipt by its original file name.
	if body := get(t, h, "/export/csv?year=2026").Body.String(); !strings.Contains(body, "Kassenbon März.png") {
		t.Error("the CSV download does not name the receipt")
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The settings offer the exports for a year of choice; each button reaches
// its export with that year (the form submits ?year=).
func TestSettingsOfferTheYearExports(t *testing.T) {
	h, st := newTestServer(t)
	configureIssuer(t, h, false)
	book(t, h, "2025-06-01", "outgoing", "Vorjahr", "10,00")
	// An invoice from long ago, e.g. imported from an old program, brings its
	// year along even with no booking in it.
	contactID := createContact(t, h, st)
	if _, err := st.CreateInvoice(t.Context(), store.NewInvoice{ContactID: contactID, IssueDate: "2012-03-01", ServiceDate: "2012-03-01",
		Lines: []store.InvoiceLine{{Description: "Alt", QuantityMilli: 1000, UnitPriceCents: 100, VATRate: 20}}}); err != nil {
		t.Fatal(err)
	}
	body := get(t, h, "/settings").Body.String()
	oldest := strconv.Itoa(time.Now().Year() - store.RetentionYears)
	for _, want := range []string{`action="/export/csv"`, `formaction="/export/archive"`, `<option value="2025" selected>`,
		`<option value="` + oldest + `"`, `<option value="2012"`, `<option value="2016"`} {
		if !strings.Contains(body, want) {
			t.Errorf("settings lack %s", want)
		}
	}
	if strings.Contains(body, `formaction="/export/datev"`) {
		t.Error("the DATEV button is shown while DATEV is off")
	}
	if rec := get(t, h, "/export/archive?year=2025"); rec.Code != 200 {
		t.Errorf("archive 2025 = %d", rec.Code)
	}
}
