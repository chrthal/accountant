package server_test

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// ADR 0035: a transfer between the business and its owner is marked private,
// not booked; it leaves the open list and counts in no total.
func TestPrivateTransfers(t *testing.T) {
	h, st := newTestServer(t)
	ctx := t.Context()
	configureIssuer(t, h, false) // issuer "Mustermann IT"
	importThroughUI(t, h, "Datum;Betrag;Empfänger;Zweck\n"+
		"05.03.2026;-500,00;IT Mustermann;Entnahme\n"+
		"06.03.2026;-49,90;BILLA;Einkauf\n"+
		"07.03.2026;1000,00;Mustermann IT;Einlage\n")

	bank := get(t, h, "/bank").Body.String()
	if !strings.Contains(bank, "2 Überweisung(en) an oder von Ihnen selbst") {
		t.Fatal("own transfers are not recognised")
	}
	if drafts := get(t, h, "/bank/drafts").Body.String(); strings.Contains(drafts, "IT Mustermann") || !strings.Contains(drafts, "BILLA") {
		t.Error("the booking suggestions must leave own transfers out")
	}

	if rec := post(t, h, "/bank/private-own", url.Values{}); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "private=2") {
		t.Fatalf("mark own = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if n, _ := st.CountUnmatchedBank(ctx); n != 1 {
		t.Errorf("open bank lines = %d, want 1 (BILLA)", n)
	}
	if all := get(t, h, "/bank?all=1").Body.String(); strings.Count(all, "Privat aufheben") != 2 {
		t.Error("the private lines are not shown as private")
	}
	reports := get(t, h, "/reports?year=2026").Body.String()
	if !strings.Contains(reports, "500,00 €") || !strings.Contains(reports, "1.000,00 €") || !strings.Contains(reports, "Privatentnahmen") {
		t.Error("the report lacks the private totals")
	}
	totals, _ := st.Totals(ctx, 2026)
	if totals.IncomeGrossCents != 0 || totals.OutgoingGrossCents != 0 {
		t.Errorf("private lines changed the totals: %+v", totals)
	}

	txs, _ := st.ListBankTransactions(ctx, false, 10)
	var private, billa int64
	for _, tx := range txs {
		switch tx.Counterparty {
		case "IT Mustermann":
			private = tx.ID
		case "BILLA":
			billa = tx.ID
		}
	}
	book(t, h, "2026-03-05", "outgoing", "Versuch", "500,00")
	if rec := post(t, h, "/bank/"+strconv.FormatInt(private, 10)+"/match", url.Values{"entry_id": {"1"}}); rec.Code == http.StatusSeeOther {
		t.Error("a private line was matched to a booking")
	}

	// A matched line cannot become private.
	book(t, h, "2026-03-06", "outgoing", "Einkauf", "49,90")
	post(t, h, "/bank/"+strconv.FormatInt(billa, 10)+"/match", url.Values{"entry_id": {"2"}})
	if rec := post(t, h, "/bank/"+strconv.FormatInt(billa, 10)+"/private", url.Values{}); rec.Code == http.StatusSeeOther {
		t.Error("a matched line was marked private")
	}

	post(t, h, "/bank/"+strconv.FormatInt(private, 10)+"/unprivate", url.Values{})
	if n, _ := st.CountUnmatchedBank(ctx); n != 1 {
		t.Errorf("after undo, open lines = %d, want 1", n)
	}
}

// ADR 0035: transfers to or from the owner that were booked as income or
// expense are moved to private in one step; invoice payments stay.
func TestConvertBookedTransfersToPrivate(t *testing.T) {
	h, st := newTestServer(t)
	ctx := t.Context()
	configureIssuer(t, h, false) // issuer "Mustermann IT"
	contactID := createContact(t, h, st)
	post(t, h, "/invoices", invoiceForm(contactID, [5]string{"Beratung", "1", "Std.", "100,00", "20"})) // 120,00, 2026-0001
	importThroughUI(t, h, "Datum;Betrag;Empfänger;Zweck\n"+
		"18.02.2026;-200,00;Mustermann IT;Entnahme\n"+
		"25.03.2026;-150,00;IT Mustermann;Entnahme\n"+
		"01.04.2026;500,00;Mustermann IT;Einlage\n"+
		"02.04.2026;120,00;Mustermann IT;RE 2026-0001\n")

	// Booked by hand as an expense or income, as one would without the
	// private flag; the last one pays the invoice.
	txs, _ := st.ListBankTransactions(ctx, true, 10)
	byPurpose := map[string]int64{}
	for _, tx := range txs {
		byPurpose[tx.BookedOn+" "+tx.Purpose] = tx.ID
	}
	match := func(key, date, dir, amount string) {
		t.Helper()
		book(t, h, date, dir, "Mustermann", amount)
		entries, _ := st.ListEntries(ctx, 1)
		post(t, h, "/bank/"+strconv.FormatInt(byPurpose[key], 10)+"/match", url.Values{"entry_id": {strconv.FormatInt(entries[0].ID, 10)}})
	}
	match("2026-02-18 Entnahme", "2026-02-18", "outgoing", "200,00")
	match("2026-03-25 Entnahme", "2026-03-25", "outgoing", "150,00")
	match("2026-04-01 Einlage", "2026-04-01", "income", "500,00")
	post(t, h, "/bank/"+strconv.FormatInt(byPurpose["2026-04-02 RE 2026-0001"], 10)+"/invoice", url.Values{"invoice_id": {"1"}})

	if bank := get(t, h, "/bank").Body.String(); !strings.Contains(bank, "3 Überweisung(en) an oder von Ihnen selbst sind als Einnahme oder Ausgabe gebucht") {
		t.Fatal("the booked own transfers are not named")
	}
	rec := post(t, h, "/bank/private-own-booked", url.Values{})
	if rec.Code != http.StatusSeeOther || !strings.HasSuffix(rec.Header().Get("Location"), "converted=3") {
		t.Fatalf("convert all = %d %s", rec.Code, rec.Header().Get("Location"))
	}

	tot, _ := st.Totals(ctx, 2026)
	if tot.OutgoingGrossCents != 0 || tot.IncomeGrossCents != 12000 {
		t.Errorf("after converting: outgoing %d, income %d; want 0 and the invoice's 120,00", tot.OutgoingGrossCents, tot.IncomeGrossCents)
	}
	if inv, _ := st.GetInvoice(ctx, 1); !inv.IsPaid() {
		t.Error("the invoice payment was undone")
	}
	all, _ := st.ListEntries(ctx, 0)
	stornos := 0
	for _, e := range all {
		if e.IsReversal() {
			stornos++
			orig, _ := st.GetEntry(ctx, *e.ReversesEntryID)
			if e.BookedOn != orig.BookedOn {
				t.Errorf("Storno #%d on %s, its booking on %s: the year's figures need the same date", e.ID, e.BookedOn, orig.BookedOn)
			}
		}
	}
	if stornos != 3 {
		t.Errorf("Stornos = %d, want 3", stornos)
	}
	private, _ := st.PrivateTotalsFor(ctx, 2026)
	if private.WithdrawalCents != 35000 || private.DepositCents != 50000 {
		t.Errorf("private totals = %+v", private)
	}
	if bank := get(t, h, "/bank").Body.String(); strings.Contains(bank, "sind als Einnahme oder Ausgabe gebucht") {
		t.Error("the warning is still shown, although only the invoice payment is left")
	}
	// Asked directly, the invoice payment is refused, and nothing changes.
	rec = post(t, h, "/bank/"+strconv.FormatInt(byPurpose["2026-04-02 RE 2026-0001"], 10)+"/to-private", url.Values{})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Rechnungszahlung gehört in die Bücher") {
		t.Errorf("converting an invoice payment = %d", rec.Code)
	}
	if inv, _ := st.GetInvoice(ctx, 1); !inv.IsPaid() {
		t.Error("the refused conversion changed the invoice")
	}
}
