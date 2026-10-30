package server_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/store"
)

func uploadInvoices(t *testing.T, h http.Handler, files map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for name, data := range files {
		part, _ := mw.CreateFormFile("originals", name)
		part.Write(data)
	}
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/invoices/import/batch", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func pdfNamed(s string) []byte { return []byte("%PDF-1.4\n% " + s + "\n%%EOF\n") }

// ADR 0032: many PDFs at once onto a waiting list, checked in one list, the
// ticked ones imported each like a single import.
func TestBulkInvoiceImport(t *testing.T) {
	h, st := newTestServer(t)
	ctx := t.Context()
	configureIssuer(t, h, false)
	contactID := createContact(t, h, st)
	if _, err := st.CreateInvoice(ctx, store.NewInvoice{ContactID: contactID, IssueDate: "2026-01-05", ServiceDate: "2026-01-05",
		Lines: []store.InvoiceLine{{Description: "Alt", QuantityMilli: 1000, UnitPriceCents: 100, VATRate: 20}}}); err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{"a.pdf": pdfNamed("a"), "b.pdf": pdfNamed("b"), "c.pdf": pdfNamed("c"), "notes.txt": []byte("no pdf")}
	rec := uploadInvoices(t, h, files)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/invoices/import/review?added=3&skipped=1" {
		t.Fatalf("batch = %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	queued, _ := st.ImportQueue(ctx, store.InvoiceQueue)
	if len(queued) != 3 {
		t.Fatalf("queue = %d, want 3", len(queued))
	}
	if body := get(t, h, "/invoices").Body.String(); !strings.Contains(body, "/invoices/import/review") {
		t.Error("the invoice list does not point to the waiting invoices")
	}
	review := get(t, h, "/invoices/import/review").Body.String()
	if strings.Count(review, `name="pick"`) != 3 || strings.Contains(review, `checked`) {
		t.Error("rows with nothing read must be listed, and none ticked")
	}

	ids := map[string]string{}
	for _, q := range queued {
		ids[q.Filename] = strconv.FormatInt(q.ID, 10)
	}
	form := url.Values{"pick": {ids["a.pdf"], ids["b.pdf"], ids["c.pdf"]}}
	row := func(file, number, date, gross, contact, newName string) {
		id := ids[file]
		form.Set("number_"+id, number)
		form.Set("issue_date_"+id, date)
		form.Set("gross_"+id, gross)
		form.Set("vat_rate_"+id, "20")
		form.Set("contact_id_"+id, contact)
		form.Set("new_contact_name_"+id, newName)
	}
	row("a.pdf", "E260201001", "01.02.2026", "1.200,00", "", "Kunde A GmbH")
	row("b.pdf", "E260202001", "02.02.2026", "500,00", "", "Kunde A GmbH")
	row("c.pdf", "2026-0001", "03.02.2026", "100,00", strconv.FormatInt(contactID, 10), "")
	rec = post(t, h, "/invoices/import/review", form)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "2 importiert, 1 nicht") {
		t.Fatalf("import = %d: %s", rec.Code, rec.Body.String())
	}

	invs, _ := st.ListInvoices(ctx, 10)
	if len(invs) != 3 {
		t.Fatalf("invoices = %d, want 3", len(invs))
	}
	for _, inv := range invs {
		if inv.Number == "E260201001" {
			if inv.GrossCents != 120000 || inv.RecipientName != "Kunde A GmbH" {
				t.Errorf("imported invoice = %+v", inv)
			}
			if orig, err := st.InvoiceOriginal(ctx, inv.ID); err != nil || !bytes.Equal(orig.Data, files["a.pdf"]) {
				t.Error("the original of a.pdf is not stored")
			}
		}
	}
	contacts, _ := st.ListContacts(ctx)
	n := 0
	for _, c := range contacts {
		if c.Name == "Kunde A GmbH" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("Kunde A GmbH was created %d times, want once", n)
	}
	left, _ := st.ImportQueue(ctx, store.InvoiceQueue)
	if len(left) != 1 || left[0].Filename != "c.pdf" || left[0].Fields["gross"] != "100,00" {
		t.Fatalf("waiting list after import = %+v; the failed row must stay with its edits", left)
	}
	if review := get(t, h, "/invoices/import/review").Body.String(); !strings.Contains(review, "diese Nummer gibt es schon") {
		t.Error("the duplicate number is not named")
	}

	if pdf := get(t, h, "/invoices/import/queue/"+ids["c.pdf"]+"/pdf"); pdf.Header().Get("Content-Type") != "application/pdf" ||
		!bytes.Equal(pdf.Body.Bytes(), files["c.pdf"]) {
		t.Error("the waiting original is not shown as it was uploaded")
	}

	// Open the remaining one on its own and save it there.
	edit := get(t, h, "/invoices/import/queue/"+ids["c.pdf"]).Body.String()
	if !strings.Contains(edit, `name="queue_id" value="`+ids["c.pdf"]+`"`) || strings.Contains(edit, `name="original" value=`) {
		t.Fatal("the full form does not take the original from the waiting list")
	}
	rec = post(t, h, "/invoices/import/save", url.Values{"queue_id": {ids["c.pdf"]}, "number": {"E260203001"},
		"issue_date": {"03.02.2026"}, "service_date": {"03.02.2026"}, "contact_id": {strconv.FormatInt(contactID, 10)},
		"description0": {"Beratung"}, "quantity0": {"2"}, "unit0": {"h"}, "unit_price0": {"50,00"}, "vat_rate0": {"20"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save from the waiting list = %d: %s", rec.Code, rec.Body.String())
	}
	if left, _ := st.ImportQueue(ctx, store.InvoiceQueue); len(left) != 0 {
		t.Error("the saved invoice is still on the waiting list")
	}

	// Discard.
	uploadInvoices(t, h, map[string][]byte{"d.pdf": pdfNamed("d"), "e.pdf": pdfNamed("e")})
	queued, _ = st.ImportQueue(ctx, store.InvoiceQueue)
	rec = post(t, h, "/invoices/import/review", url.Values{"discard": {strconv.FormatInt(queued[0].ID, 10)},
		"number_" + strconv.FormatInt(queued[0].ID, 10): {""}})
	if left, _ := st.ImportQueue(ctx, store.InvoiceQueue); rec.Code != http.StatusSeeOther || len(left) != 1 {
		t.Errorf("discard one = %d, %d left", rec.Code, len(left))
	}
	post(t, h, "/invoices/import/review", url.Values{"discard": {"all"}})
	if left, _ := st.ImportQueue(ctx, store.InvoiceQueue); len(left) != 0 {
		t.Error("discard all left rows behind")
	}
}
