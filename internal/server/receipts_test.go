package server_test

import (
	"bytes"
	"encoding/json"
	"html"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"

	"github.com/chrthal/accountant/internal/ocr"
)

func uploadPhoto(t *testing.T, h http.Handler, name string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("photo", name)
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	part.Write(content)
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/receipts", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The Phase 5 acceptance path: a receipt photo becomes a pre-filled draft the
// user confirms into a booking.
func TestReceiptPhotoBecomesAConfirmedBooking(t *testing.T) {
	if !ocr.Available() {
		t.Skip("tesseract is not installed — the pre-fill path cannot be exercised")
	}
	h, st := newTestServer(t)

	image, err := os.ReadFile("../ocr/testdata/receipt.png")
	if err != nil {
		t.Fatalf("read receipt image: %v", err)
	}

	rec := uploadPhoto(t, h, "receipt.png", image)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	// The recognised values must arrive as form values, not as text on a page.
	if !strings.Contains(body, `value="13,00"`) {
		t.Errorf("amount not pre-filled into the form:\n%s", body)
	}
	if !strings.Contains(body, `value="15.03.2026"`) {
		t.Error("date not pre-filled into the form")
	}
	// And the raw text must be visible, so a wrong read is obvious.
	if !strings.Contains(body, "SUMME") {
		t.Error("the recognised text is not shown for checking")
	}
	// Nothing may be saved before the user submits (ADR 0002).
	if entries, _ := st.ListEntries(t.Context(), 10); len(entries) != 0 {
		t.Fatalf("OCR saved %d bookings by itself — it must only ever draft", len(entries))
	}

	// The photo travels with the draft, so it is kept as the Beleg (ADR 0016).
	payload := regexp.MustCompile(`name="receipt_payload" value="([^"]+)"`).FindStringSubmatch(body)
	if payload == nil {
		t.Fatal("the draft does not carry the photo")
	}

	// Confirming the draft is an ordinary submit of that form.
	if rec := post(t, h, "/entries", url.Values{
		"booked_on": {"2026-03-15"}, "direction": {"outgoing"},
		"description": {"Cafe Central GmbH"}, "amount": {"13,00"},
		"vat_rate": {"10"}, "konto": {"7600"}, "gegenkonto": {"2800"},
		// A browser decodes the attribute's HTML escaping (&#43; for "+").
		"receipt_payload": {html.UnescapeString(payload[1])}, "receipt_name": {"receipt.png"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("confirm = %d, want 303", rec.Code)
	}

	entries, _ := st.ListEntries(t.Context(), 10)
	if len(entries) != 1 || entries[0].AmountGrossCents != 1300 {
		t.Fatalf("booking not saved as drafted: %+v", entries)
	}
	att, _ := st.ListAttachments(t.Context(), entries[0].ID)
	if len(att) != 1 || att[0].SizeBytes != int64(len(image)) {
		t.Errorf("the receipt photo was not kept with the booking: %+v", att)
	}
}

// A photo with nothing readable on it must still open the form, with an
// explanation, and the photo still becomes the Beleg.
func TestUnreadablePhotoStillOpensTheForm(t *testing.T) {
	h, _ := newTestServer(t)

	// A blank image: a valid photo with no text on it.
	var blank bytes.Buffer
	png.Encode(&blank, image.NewGray(image.Rect(0, 0, 64, 64)))

	rec := uploadPhoto(t, h, "leer.png", blank.Bytes())
	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d, want 200 — a failed read must not block the booking", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Buchung erfassen") {
		t.Error("the booking form was not offered after a failed recognition")
	}
	if !strings.Contains(body, "eintragen") {
		t.Error("no explanation that the values must be typed")
	}
	if !strings.Contains(body, `name="receipt_payload"`) {
		t.Error("the photo is not carried along as the Beleg")
	}
}

// A file that could never be a Beleg is refused up front, before any
// recognition runs, and the upload page says why.
func TestNonBelegUploadIsRefused(t *testing.T) {
	h, _ := newTestServer(t)
	rec := uploadPhoto(t, h, "notiz.txt", []byte("this is not an image"))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "nicht als Beleg") {
		t.Errorf("text file as receipt = %d", rec.Code)
	}
}

// The PDF path end to end: a supplier's PDF invoice pre-fills the booking
// from its text layer, and the PDF is kept as the Beleg (ADR 0018).
func TestPDFInvoiceBecomesADraft(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext (poppler) is not installed")
	}
	h, st := newTestServer(t)
	configureIssuer(t, h, false) // issuer "Mustermann IT", printed as recipient below

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Helvetica", "", 11)
	for _, l := range []string{"Papier Profi KG", "Mustermann IT", "Rechnung Nr. 4711",
		"Rechnungsdatum: 12.09.2026", "Summe netto 100,00", "USt 20 % 20,00", "Gesamtbetrag 120,00"} {
		pdf.CellFormat(180, 7, l, "", 1, "L", false, 0, "")
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		t.Fatal(err)
	}

	rec := uploadPhoto(t, h, "rechnung-4711.pdf", buf.Bytes())
	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`value="120,00"`, `value="12.09.2026"`,
		`value="Papier Profi KG – Rechnung 4711"`, "Textebene", `name="receipt_payload"`} {
		if !strings.Contains(body, want) {
			t.Errorf("draft lacks %s", want)
		}
	}
	payload := regexp.MustCompile(`name="receipt_payload" value="([^"]+)"`).FindStringSubmatch(body)
	if rec := post(t, h, "/entries", url.Values{
		"booked_on": {"2026-09-12"}, "direction": {"outgoing"}, "description": {"Papier Profi KG"},
		"amount": {"120,00"}, "vat_rate": {"20"},
		"receipt_payload": {html.UnescapeString(payload[1])}, "receipt_name": {"rechnung-4711.pdf"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("confirm = %d: %s", rec.Code, rec.Body.String())
	}
	att, _ := st.ListAttachments(t.Context(), 1)
	if len(att) != 1 || att[0].ContentType != "application/pdf" {
		t.Errorf("the PDF was not kept as the Beleg: %+v", att)
	}
}

func TestReceiptFormAlwaysAcceptsAnUpload(t *testing.T) {
	h, _ := newTestServer(t)
	body := get(t, h, "/receipts/new").Body.String()

	// The upload works with or without recognition: the file becomes the
	// Beleg either way (ADR 0018).
	if !strings.Contains(body, "fotografieren oder hochladen") || !strings.Contains(body, "application/pdf") {
		t.Error("the upload form is missing, or does not take PDFs")
	}
	// What is missing is said, not silently offered less (ADR 0011).
	if !ocr.Available() && !strings.Contains(body, "keine Texterkennung") {
		t.Error("missing OCR is not explained")
	}
}

// --- PWA ------------------------------------------------------------------

func TestManifestIsServedAndValid(t *testing.T) {
	h, _ := newTestServer(t)
	rec := get(t, h, "/static/manifest.webmanifest")
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest = %d, want 200", rec.Code)
	}

	var m struct {
		Name     string `json:"name"`
		StartURL string `json:"start_url"`
		Display  string `json:"display"`
		Icons    []struct {
			Src   string `json:"src"`
			Sizes string `json:"sizes"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if m.Name == "" || m.StartURL != "/" || m.Display != "standalone" {
		t.Errorf("manifest cannot install as an app: %+v", m)
	}
	if len(m.Icons) < 2 {
		t.Error("an installable app needs at least a 192 and a 512 icon")
	}
	for _, icon := range m.Icons {
		if rec := get(t, h, icon.Src); rec.Code != http.StatusOK {
			t.Errorf("icon %s = %d, want 200", icon.Src, rec.Code)
		}
	}
}

// The worker must come from the root, or its scope cannot cover the app.
func TestServiceWorkerIsServedFromRoot(t *testing.T) {
	h, _ := newTestServer(t)
	rec := get(t, h, "/sw.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("/sw.js = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want javascript", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q — a cached worker can never be fixed", cc)
	}

	body := rec.Body.String()
	// The one behaviour that matters: it must not cache pages.
	if strings.Contains(body, "'/'") || strings.Contains(body, `"/"`) {
		t.Error("the service worker appears to cache the app root — pages must stay network-only")
	}
	if !strings.Contains(body, "/static/app.css") {
		t.Error("the service worker does not cache the shell")
	}
}

func TestPagesLinkTheManifestAndWorker(t *testing.T) {
	h, _ := newTestServer(t)
	body := get(t, h, "/").Body.String()
	for _, want := range []string{`rel="manifest"`, `src="/static/app.js"`, `name="theme-color"`} {
		if !strings.Contains(body, want) {
			t.Errorf("layout is missing %q — the app will not install", want)
		}
	}
	// The registration lives in the static script, so a strict CSP can forbid
	// inline scripts (ADR 0013).
	if js := get(t, h, "/static/app.js").Body.String(); !strings.Contains(js, "serviceWorker.register('/sw.js')") {
		t.Error("app.js does not register the service worker")
	}
}
