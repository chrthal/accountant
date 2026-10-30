package server_test

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// ADR 0036: part payments from the invoice page and from bank lines.
func TestPartialPaymentsThroughTheUI(t *testing.T) {
	h, st := newTestServer(t)
	ctx := t.Context()
	configureIssuer(t, h, false)
	contactID := createContact(t, h, st)
	post(t, h, "/invoices", invoiceForm(contactID, [5]string{"Beratung", "1", "Std.", "100,00", "20"})) // 120,00
	post(t, h, "/invoices", invoiceForm(contactID, [5]string{"Beratung", "1", "Std.", "100,00", "20"})) // 120,00

	if page := get(t, h, "/invoices/1").Body.String(); !strings.Contains(page, `name="amount"`) || !strings.Contains(page, `value="120,00"`) {
		t.Fatal("the payment form has no amount, prefilled with what is open")
	}
	if rec := post(t, h, "/invoices/1/payment", url.Values{"paid_on": {"01.09.2026"}, "amount": {"130,00"}}); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "höher als der offene Betrag") {
		t.Errorf("overpayment = %d", rec.Code)
	}
	if rec := post(t, h, "/invoices/1/payment", url.Values{"paid_on": {"01.09.2026"}, "amount": {"50,00"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("part payment = %d: %s", rec.Code, rec.Body.String())
	}
	if list := get(t, h, "/invoices").Body.String(); !strings.Contains(list, "teilweise bezahlt") {
		t.Error("the list does not show the invoice as partly paid")
	}

	// From the bank: a transfer naming invoice 2 with less, then the rest.
	importThroughUI(t, h, "Datum;Betrag;Empfänger;Zweck\n"+
		"10.09.2026;70,00;Muster GmbH;RE 2026-0002 erste Rate\n"+
		"20.09.2026;50,00;Muster GmbH;RE 2026-0002 Rest\n")
	bank := get(t, h, "/bank").Body.String()
	if !strings.Contains(bank, "Teilzahlung Rechnung 2026-0002") {
		t.Fatal("the bank page does not offer the part payment")
	}
	txs, _ := st.ListBankTransactions(ctx, true, 10)
	ids := map[string]int64{}
	for _, tx := range txs {
		ids[tx.Purpose] = tx.ID
	}
	for _, purpose := range []string{"RE 2026-0002 erste Rate", "RE 2026-0002 Rest"} {
		id, ok := ids[purpose]
		if !ok {
			t.Fatalf("transaction %q not found in %v", purpose, ids)
		}
		if rec := post(t, h, "/bank/"+strconv.FormatInt(id, 10)+"/invoice", url.Values{"invoice_id": {"2"}}); rec.Code != http.StatusSeeOther {
			t.Fatalf("pay from %q = %d: %s", purpose, rec.Code, rec.Body.String())
		}
	}
	inv, _ := st.GetInvoice(ctx, 2)
	if !inv.IsPaid() || inv.PaidCents != 12000 {
		t.Errorf("invoice 2 after both transfers: paid %d", inv.PaidCents)
	}
	if n, _ := st.CountUnmatchedBank(ctx); n != 0 {
		t.Errorf("%d bank lines left open", n)
	}
}
