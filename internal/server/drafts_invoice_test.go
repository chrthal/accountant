package server_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// ADR 0026: a draft keeps half-finished input, can be previewed without
// taking a number, and disappears when it is issued.
func TestInvoiceDrafts(t *testing.T) {
	h, st := newTestServer(t)
	ctx := context.Background()
	configureIssuer(t, h, false)
	contactID := createContact(t, h, st)

	// Half-finished: no recipient, a price still being typed. Saved anyway.
	form := invoiceForm(0, [5]string{"Website", "1", "Stk.", "12,", "20"})
	form.Set("contact_id", "")
	form.Set("action", "draft")
	if rec := post(t, h, "/invoices", form); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "draft=saved") {
		t.Fatalf("save draft = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	list := get(t, h, "/invoices").Body.String()
	if !strings.Contains(list, "/invoice-drafts/1") || !strings.Contains(list, "ohne Empfänger") {
		t.Error("the draft is not listed")
	}
	edit := get(t, h, "/invoice-drafts/1").Body.String()
	for _, want := range []string{`name="draft_id" value="1"`, `value="Website"`, `value="12,"`, "Entwurf bearbeiten"} {
		if !strings.Contains(edit, want) {
			t.Errorf("draft form lacks %s", want)
		}
	}

	// Finished and saved again: still one draft, now with its recipient.
	form = invoiceForm(contactID, [5]string{"Website", "1", "Stk.", "1700,00", "20"})
	form.Set("draft_id", "1")
	form.Set("note", "*Spezifikation\nStatische Website")
	form.Set("action", "draft")
	post(t, h, "/invoices", form)
	drafts, err := st.ListDrafts(ctx)
	if err != nil || len(drafts) != 1 || drafts[0].Fields["contact_id"] != strconv.FormatInt(contactID, 10) {
		t.Fatalf("drafts after update = %+v, %v", drafts, err)
	}
	if !strings.Contains(get(t, h, "/invoices").Body.String(), "2.040,00") {
		t.Error("the list does not show the draft's total")
	}

	// Preview: a PDF, and no invoice number used up.
	form.Set("action", "preview")
	rec := post(t, h, "/invoices", form)
	if rec.Header().Get("Content-Type") != "application/pdf" || !strings.HasPrefix(rec.Body.String(), "%PDF-") {
		t.Fatalf("preview = %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if invs, _ := st.ListInvoices(ctx, 10); len(invs) != 0 {
		t.Fatal("the preview issued an invoice")
	}
	form.Set("unit_price0", "abc")
	if rec := post(t, h, "/invoices", form); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("preview of a broken price = %d, want 422", rec.Code)
	}
	form.Set("unit_price0", "1700,00")

	// Issue: the invoice gets its number and the draft is gone.
	form.Set("action", "issue")
	if rec := post(t, h, "/invoices", form); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/invoices/1" {
		t.Fatalf("issue = %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if drafts, _ := st.ListDrafts(ctx); len(drafts) != 0 {
		t.Error("the draft is still there after issuing")
	}
	inv, err := st.GetInvoice(ctx, 1)
	if err != nil || inv.Number != "2026-0001" || !strings.Contains(inv.Note, "Statische Website") {
		t.Errorf("issued invoice = %+v, %v", inv, err)
	}
	// Saving the issued draft once more, e.g. from a second tab, says so.
	form.Set("action", "draft")
	if rec := post(t, h, "/invoices", form); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("saving an issued draft = %d, want 422", rec.Code)
	}
	if rec := get(t, h, "/invoice-drafts/1"); rec.Code != http.StatusSeeOther {
		t.Errorf("opening an issued draft = %d, want a redirect", rec.Code)
	}

	// Delete.
	form.Del("draft_id")
	post(t, h, "/invoices", form)
	drafts, _ = st.ListDrafts(ctx)
	if len(drafts) != 1 {
		t.Fatalf("drafts = %d, want 1", len(drafts))
	}
	if rec := post(t, h, "/invoice-drafts/"+strconv.FormatInt(drafts[0].ID, 10)+"/delete", url.Values{}); rec.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", rec.Code)
	}
	if drafts, _ := st.ListDrafts(ctx); len(drafts) != 0 {
		t.Error("the draft was not deleted")
	}
}
