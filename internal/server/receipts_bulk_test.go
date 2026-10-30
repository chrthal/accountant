package server_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chrthal/accountant/internal/store"
)

func uploadReceipts(t *testing.T, h http.Handler, files [][2]string, data map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, f := range files {
		part, _ := mw.CreateFormFile("photos", f[0])
		part.Write(data[f[1]])
	}
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/receipts/batch", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// ADR 0033: many Belege onto a waiting list, checked in one list, each
// ticked one booked with its Beleg; a receipt booked already is named.
func TestBulkReceipts(t *testing.T) {
	h, st := newTestServer(t)
	ctx := t.Context()
	png, err := os.ReadFile("../ocr/testdata/receipt.png")
	if err != nil {
		t.Fatal(err)
	}
	data := map[string][]byte{"png": png, "pdf": pdfNamed("rechnung"), "txt": []byte("no receipt")}
	rec := uploadReceipts(t, h, [][2]string{{"bon.png", "png"}, {"bon-kopie.png", "png"}, {"rechnung.pdf", "pdf"}, {"notiz.txt", "txt"}}, data)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/receipts/review?added=3&skipped=1" {
		t.Fatalf("batch = %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	// The upload only stores; the list shows the Belege as being read.
	if early := get(t, h, "/receipts/review").Body.String(); !strings.Contains(early, "data-refresh") && !readDone(st) {
		t.Error("Belege being read are not shown as such")
	}
	waitRead(t, st)
	if strings.Contains(get(t, h, "/receipts/review").Body.String(), "data-refresh") {
		t.Error("the list still reloads after everything was read")
	}
	queued, _ := st.ImportQueue(ctx, store.ReceiptQueue)
	ids := map[string]string{}
	for _, q := range queued {
		ids[q.Filename] = strconv.FormatInt(q.ID, 10)
	}
	review := get(t, h, "/receipts/review").Body.String()
	if strings.Count(review, "dieselbe Datei ist mehrfach in der Liste") != 2 || strings.Contains(review, "checked") {
		t.Error("the same photo twice must be named on both rows, and nothing ticked")
	}
	if !strings.Contains(review, `src="/receipts/queue/`+ids["bon.png"]+`/file"`) || strings.Contains(review, `src="/receipts/queue/`+ids["rechnung.pdf"]+`/file"`) {
		t.Error("photos need a preview, PDFs a link only")
	}
	if f := get(t, h, "/receipts/queue/"+ids["bon.png"]+"/file"); f.Header().Get("Content-Type") != "image/png" || !bytes.Equal(f.Body.Bytes(), png) {
		t.Error("the waiting photo is not served as uploaded")
	}
	if !strings.Contains(get(t, h, "/entries").Body.String(), "/receipts/review") {
		t.Error("the bookings page does not point to the waiting receipts")
	}

	form := url.Values{"pick": {ids["bon.png"]}}
	for file, id := range ids {
		form.Set("booked_on_"+id, "15.03.2026")
		form.Set("amount_"+id, "13,00")
		form.Set("vat_rate_"+id, "20")
		form.Set("direction_"+id, "outgoing")
		form.Set("description_"+id, "Papier "+file)
		form.Set("category_id_"+id, "")
		form.Set("contact_id_"+id, "")
	}
	rec = post(t, h, "/receipts/review", form)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "booked=1") {
		t.Fatalf("book = %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	entries, _ := st.ListEntries(ctx, 0)
	if len(entries) != 1 || entries[0].AmountGrossCents != 1300 || entries[0].Description != "Papier bon.png" {
		t.Fatalf("entries = %+v", entries)
	}
	if att, _ := st.ListAttachments(ctx, entries[0].ID); len(att) != 1 || att[0].Filename != "bon.png" {
		t.Errorf("the booking lacks its receipt: %+v", att)
	}
	if review := get(t, h, "/receipts/review").Body.String(); !strings.Contains(review, "dieser Beleg ist schon gebucht (Buchung #1)") {
		t.Error("the copy of a booked receipt is not named")
	}
	// Edits survive: the copy keeps the description typed for it.
	left, _ := st.ImportQueue(ctx, store.ReceiptQueue)
	for _, q := range left {
		if q.Filename == "bon-kopie.png" && q.Fields["description"] != "Papier bon-kopie.png" {
			t.Errorf("typed description lost: %v", q.Fields)
		}
	}

	// Open the PDF in the ordinary form and book it there.
	edit := get(t, h, "/receipts/queue/"+ids["rechnung.pdf"]).Body.String()
	if !strings.Contains(edit, `name="receipt_queue_id" value="`+ids["rechnung.pdf"]+`"`) || strings.Contains(edit, `name="receipt" accept`) {
		t.Fatal("the form does not take the receipt from the waiting list")
	}
	rec = post(t, h, "/entries", url.Values{"receipt_queue_id": {ids["rechnung.pdf"]}, "receipt_name": {"rechnung.pdf"},
		"booked_on": {"16.03.2026"}, "direction": {"outgoing"}, "description": {"Lieferant"}, "amount": {"120,00"}, "vat_rate": {"20"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("book from the form = %d: %s", rec.Code, rec.Body.String())
	}
	if att, _ := st.ListAttachments(ctx, 2); len(att) != 1 || att[0].Filename != "rechnung.pdf" {
		t.Errorf("the form booking lacks its receipt: %+v", att)
	}

	post(t, h, "/receipts/review", url.Values{"discard": {"all"}})
	if left, _ := st.ImportQueue(ctx, store.ReceiptQueue); len(left) != 0 {
		t.Error("discard all left rows behind")
	}
}

func readDone(st *store.Store) bool {
	queued, _ := st.ImportQueue(context.Background(), store.ReceiptQueue)
	for _, q := range queued {
		if q.Fields["pending"] != "" {
			return false
		}
	}
	return true
}

// waitRead waits until the background reader has read every waiting Beleg.
func waitRead(t *testing.T, st *store.Store) {
	t.Helper()
	for end := time.Now().Add(60 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if readDone(st) {
			return
		}
	}
	t.Fatal("the Belege were not read in time")
}
