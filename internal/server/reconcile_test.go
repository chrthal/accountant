package server_test

import (
	"bytes"
	"encoding/base64"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Shaped like the meinELBA export that exposed the header bug: no header row,
// and the first line is already a transaction. Values are made up.
const headerlessExport = `03.08.2026;Empfänger: Hosting AG Verwendungszweck: Server August;03.08.2026;-540,00;EUR;03.08.2026 10:11:12:123
05.08.2026;Auftraggeber: Muster GmbH Verwendungszweck: Honorar Juli;05.08.2026;1.200,00;EUR;05.08.2026 08:00:01:000
`

func selected(body, name string) string {
	m := regexp.MustCompile(`(?s)<select name="` + name + `".*?<option value="(-?\d+)"[^>]*selected`).FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

// Phase 9 acceptance, first half: the headerless export is recognised, and
// the next import of the same layout starts from the remembered mapping.
func TestHeaderlessImportIsGuessedAndRemembered(t *testing.T) {
	h, st := newTestServer(t)
	up := uploadCSV(t, h, headerlessExport)
	body := up.Body.String()
	if strings.Contains(body, `name="has_header" value="1" checked`) {
		t.Error("the header box is checked for a file without a header")
	}
	if selected(body, "col_date") != "0" || selected(body, "col_amount") != "3" || selected(body, "col_purpose") != "1" {
		t.Errorf("preselected date/amount/purpose = %s/%s/%s, want 0/3/1",
			selected(body, "col_date"), selected(body, "col_amount"), selected(body, "col_purpose"))
	}

	payload := base64.StdEncoding.EncodeToString([]byte(headerlessExport))
	rec := post(t, h, "/bank/import", url.Values{
		"payload": {payload}, "source_name": {"elba.csv"},
		"col_date": {"0"}, "col_amount": {"3"}, "col_counterparty": {"-1"}, "col_purpose": {"2"},
	})
	if !strings.Contains(rec.Body.String(), "2 Umsätze importiert") {
		t.Fatalf("import did not take both rows: %s", rec.Body.String())
	}
	txs, _ := st.ListBankTransactions(t.Context(), false, 10)
	if len(txs) != 2 {
		t.Fatalf("transactions = %d, want 2 — the first row must not be eaten as a header", len(txs))
	}

	// The user chose column 2 for the purpose; next time that is preselected.
	again := uploadCSV(t, h, headerlessExport).Body.String()
	if !strings.Contains(again, "wie beim letzten Import") || selected(again, "col_purpose") != "2" {
		t.Error("the confirmed mapping was not remembered")
	}
}

func TestBookingFromABankLine(t *testing.T) {
	h, st := newTestServer(t)
	importThroughUI(t, h, bankExport)
	txs, _ := st.ListBankTransactions(t.Context(), true, 10)
	var out int64
	for _, tx := range txs {
		if tx.AmountCents < 0 {
			out = tx.ID
		}
	}

	bank := get(t, h, "/bank").Body.String()
	if !strings.Contains(bank, "/entries/new?bank_tx="+strconv.FormatInt(out, 10)) {
		t.Error("the bank page offers no 'Buchung anlegen'")
	}

	form := get(t, h, "/entries/new?bank_tx="+strconv.FormatInt(out, 10)).Body.String()
	for _, want := range []string{`value="1.234,56"`, `value="15.03.2026"`, `value="Büro Müller GmbH"`,
		`name="bank_tx_id" value="` + strconv.FormatInt(out, 10) + `"`} {
		if !strings.Contains(form, want) {
			t.Errorf("prefilled form lacks %s", want)
		}
	}

	rec := post(t, h, "/entries", url.Values{
		"booked_on": {"2026-03-15"}, "direction": {"outgoing"}, "description": {"Büro Müller GmbH"},
		"amount": {"1.234,56"}, "vat_rate": {"20"}, "bank_tx_id": {strconv.FormatInt(out, 10)},
	})
	if rec.Code != http.StatusSeeOther || strings.Contains(rec.Header().Get("Location"), "warn") {
		t.Fatalf("create = %d → %s", rec.Code, rec.Header().Get("Location"))
	}
	tx, _ := st.GetBankTransaction(t.Context(), out)
	if !tx.IsMatched() {
		t.Error("the new booking was not matched to its bank line")
	}

	// Change the amount on the way: the booking is saved, the match refused,
	// and the booking page says so instead of failing silently.
	other := txs[0].ID
	if other == out {
		other = txs[1].ID
	}
	rec = post(t, h, "/entries", url.Values{
		"booked_on": {"2026-03-16"}, "direction": {"income"}, "description": {"Muster"},
		"amount": {"1.999,00"}, "vat_rate": {"20"}, "bank_tx_id": {strconv.FormatInt(other, 10)},
	})
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "warn=bank") {
		t.Fatalf("mismatch not reported: %s", loc)
	}
	if !strings.Contains(get(t, h, loc).Body.String(), "passt aber nicht zum Bankumsatz") {
		t.Error("the booking page does not explain the failed match")
	}
}

// Phase 9 acceptance, second half: one transfer reconciles a mixed-rate invoice.
func TestInvoicePaidFromTheBankPage(t *testing.T) {
	h, st := newTestServer(t)
	configureIssuer(t, h, false)
	contactID := createContact(t, h, st)
	post(t, h, "/invoices", invoiceForm(contactID,
		[5]string{"Beratung", "1,5", "Std.", "100,00", "20"},
		[5]string{"Verpflegung", "2", "Stk.", "25,00", "10"},
	)) // gross 235,00
	importThroughUI(t, h, "Datum;Betrag;Empfänger;Zweck\n20.08.2026;235,00;Muster GmbH;RE 2026-0001\n")

	bank := get(t, h, "/bank").Body.String()
	if !strings.Contains(bank, "Rechnung 2026-0001 bezahlt") {
		t.Fatal("the matching open invoice is not suggested")
	}
	txs, _ := st.ListBankTransactions(t.Context(), true, 10)
	rec := post(t, h, "/bank/"+strconv.FormatInt(txs[0].ID, 10)+"/invoice", url.Values{"invoice_id": {"1"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("pay from bank = %d: %s", rec.Code, rec.Body.String())
	}
	inv, _ := st.GetInvoice(t.Context(), 1)
	if !inv.IsPaid() || inv.LastPaidOn != "2026-08-20" {
		t.Errorf("invoice paid %v on %q, want paid on the transfer date", inv.IsPaid(), inv.LastPaidOn)
	}
	tx, _ := st.GetBankTransaction(t.Context(), txs[0].ID)
	if len(tx.Matches) != 2 {
		t.Errorf("transfer matches %d entries, want both VAT-rate entries", len(tx.Matches))
	}
	// Doing it again is refused before anything is booked.
	if rec := post(t, h, "/bank/"+strconv.FormatInt(txs[0].ID, 10)+"/invoice", url.Values{"invoice_id": {"1"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("second pay-from-bank = %d, want 422", rec.Code)
	}
	if entries, _ := st.EntriesForInvoice(t.Context(), 1); len(entries) != 2 {
		t.Errorf("entries for invoice = %d, want 2", len(entries))
	}
}

func postMultipart(t *testing.T, h http.Handler, path string, fields map[string]string, fileField, fileName string, file []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	if fileField != "" {
		part, _ := mw.CreateFormFile(fileField, fileName)
		part.Write(file)
	}
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Phase 10: a Beleg uploaded with the booking is kept, served back safely,
// and a booking without one is flagged.
func TestReceiptsAreStoredWithTheBooking(t *testing.T) {
	h, st := newTestServer(t)
	png, err := os.ReadFile("../ocr/testdata/receipt.png")
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{"booked_on": "2026-03-15", "direction": "outgoing",
		"description": "Papier", "amount": "13,00", "vat_rate": "20"}

	rec := postMultipart(t, h, "/entries", fields, "receipt", "kassenbon.png", png)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("book with receipt = %d: %s", rec.Code, rec.Body.String())
	}
	list, _ := st.ListAttachments(t.Context(), 1)
	if len(list) != 1 || list[0].Filename != "kassenbon.png" {
		t.Fatalf("attachments = %+v", list)
	}

	served := get(t, h, "/attachments/"+strconv.FormatInt(list[0].ID, 10))
	if served.Code != 200 || !bytes.Equal(served.Body.Bytes(), png) {
		t.Fatalf("serve = %d", served.Code)
	}
	if served.Header().Get("Content-Type") != "image/png" || served.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(served.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("attachment headers = %v", served.Header())
	}

	// A booking without a Beleg is flagged, and one can be added later.
	delete(fields, "x")
	fields["description"] = "Ohne Beleg"
	postMultipart(t, h, "/entries", fields, "", "", nil)
	if body := get(t, h, "/entries?beleg=fehlt").Body.String(); !strings.Contains(body, "Ohne Beleg") || strings.Contains(body, ">Papier") {
		t.Error("the 'ohne Beleg' filter is wrong")
	}
	if body := get(t, h, "/").Body.String(); !strings.Contains(body, "Belege fehlen") {
		t.Error("the overview does not list missing receipts")
	}
	rec = postMultipart(t, h, "/entries/2/attachments", nil, "receipt", "nachgereicht.png", png)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("add later = %d: %s", rec.Code, rec.Body.String())
	}
	if body := get(t, h, "/entries/2").Body.String(); !strings.Contains(body, "/attachments/2") {
		t.Error("the booking page does not show the added Beleg")
	}

	// Anything that is not a photo or PDF is refused, with the form intact.
	fields["description"] = "Böse"
	rec = postMultipart(t, h, "/entries", fields, "receipt", "x.jpg", []byte("<html><script>alert(1)</script>"))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Böse") {
		t.Errorf("HTML as receipt = %d", rec.Code)
	}
	if p, _ := st.ListEntries(t.Context(), 10); len(p) != 2 {
		t.Errorf("a refused receipt still created a booking (%d entries)", len(p))
	}
}

func TestVATPageShowsTheQuarter(t *testing.T) {
	h, _ := newTestServer(t)
	book(t, h, "2026-01-10", "income", "Honorar", "1.200,00")
	book(t, h, "2026-02-10", "outgoing", "Laptop", "240,00")
	body := get(t, h, "/reports/vat?year=2026").Body.String()
	for _, want := range []string{"1. Quartal", "200,00", "40,00", "160,00", "keine Umsatzsteuervoranmeldung", "U30"} {
		if !strings.Contains(body, want) {
			t.Errorf("VAT page lacks %q", want)
		}
	}
	if body := get(t, h, "/reports/vat?year=2026&period=month").Body.String(); !strings.Contains(body, "Februar") {
		t.Error("monthly view missing")
	}
}

// ADR 0020: the bulk action books only what is unambiguous.
func TestAutoMatchBooksOnlyUnambiguousTransfers(t *testing.T) {
	h, st := newTestServer(t)
	configureIssuer(t, h, false)
	contactID := createContact(t, h, st) // "Muster GmbH"
	for range 3 {
		post(t, h, "/invoices", invoiceForm(contactID, [5]string{"Beratung", "1", "Std.", "100,00", "20"})) // 120,00 each
	}
	importThroughUI(t, h, "Datum;Betrag;Empfänger;Zweck\n"+
		"20.08.2026;120,00;MUSTER GMBH WIEN;RE 2026-0001\n"+ // number + amount: sure
		"21.08.2026;120,00;Muster GmbH;Danke\n"+ // amount only: three candidates
		"22.08.2026;60,00;Muster GmbH;Teilzahlung 2026-0003\n") // number, short amount

	bank := get(t, h, "/bank").Body.String()
	if !strings.Contains(bank, "Alle eindeutigen zuordnen") || !strings.Contains(bank, "Rechnungsnummer im Text") {
		t.Fatal("the bank page does not offer the bulk action or explain the hint")
	}
	if !strings.Contains(bank, "Teilzahlung Rechnung 2026-0003") {
		t.Error("the short payment is not offered as a part payment")
	}

	rec := post(t, h, "/bank/auto", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/bank?auto=1" {
		t.Fatalf("auto match = %d → %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if inv, _ := st.GetInvoice(t.Context(), 1); !inv.IsPaid() || inv.LastPaidOn != "2026-08-20" {
		t.Errorf("invoice 1 paid=%v on %q", inv.IsPaid(), inv.LastPaidOn)
	}
	for _, id := range []int64{2, 3} {
		if inv, _ := st.GetInvoice(t.Context(), id); inv.IsPaid() || inv.PaidCents != 0 {
			t.Errorf("invoice %d was booked although its transfer was ambiguous", id)
		}
	}
	if n, _ := st.CountUnmatchedBank(t.Context()); n != 2 {
		t.Errorf("unmatched after auto = %d, want 2", n)
	}
	if body := get(t, h, "/bank?auto=1").Body.String(); !strings.Contains(body, "1 Zahlungseingänge eindeutig zugeordnet") {
		t.Error("the result is not reported")
	}
}
