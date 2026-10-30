package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/store"
)

// ADR 0041: the booking form takes a business share, the booking page shows
// it, and next month's bank line starts from it.
func TestBusinessShareForm(t *testing.T) {
	h, st := newTestServer(t)
	importThroughUI(t, h, "Datum;Betrag;Empfänger;Zweck\n03.08.2026;-60,00;A1 Telekom;Rechnung August\n")
	txs, _ := st.ListBankTransactions(t.Context(), true, 10)

	if rec := post(t, h, "/entries", url.Values{"booked_on": {"2026-08-03"}, "direction": {"outgoing"},
		"description": {"Handy"}, "amount": {"60,00"}, "vat_rate": {"20"}, "business_pct": {"0"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("0 %% accepted: %d", rec.Code)
	}
	rec := post(t, h, "/entries", url.Values{"booked_on": {"2026-08-03"}, "direction": {"outgoing"},
		"description": {"Handy"}, "amount": {"60,00"}, "vat_rate": {"20"}, "business_pct": {"60"},
		"business_note": {"Einzelverbindungsnachweis"}, "bank_tx_id": {i(txs[0].ID)}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("booking: %d %s", rec.Code, rec.Body.String())
	}
	page := get(t, h, rec.Header().Get("Location")).Body.String()
	for _, want := range []string{"60 % betrieblich, 40 % privat", "Einzelverbindungsnachweis", "davon Vorsteuer", "6,00 €"} {
		if !strings.Contains(page, want) {
			t.Errorf("booking page lacks %q", want)
		}
	}

	post(t, h, "/entries", url.Values{"booked_on": {"2026-08-04"}, "direction": {"income"},
		"description": {"Honorar"}, "amount": {"100,00"}, "vat_rate": {"20"}, "business_pct": {"50"}})
	if e, _ := st.ListEntries(t.Context(), 1); e[0].BusinessPct != 100 {
		t.Errorf("income stored with a share of %d %%", e[0].BusinessPct)
	}

	importThroughUI(t, h, "Datum;Betrag;Empfänger;Zweck\n03.09.2026;-60,00;A1 Telekom;Rechnung September\n")
	txs, _ = st.ListBankTransactions(t.Context(), true, 10)
	var next int64
	for _, tx := range txs {
		if !tx.IsMatched() {
			next = tx.ID
		}
	}
	form := get(t, h, "/entries/new?bank_tx="+i(next)).Body.String()
	if !strings.Contains(form, `name="business_pct" min="1" max="100" step="1" inputmode="numeric" value="60"`) {
		t.Error("the September form does not start from last month's share")
	}
}

// The business share is a field in both bulk lists: Belege and bank drafts.
func TestBusinessShareInBulkLists(t *testing.T) {
	h, st := newTestServer(t)
	uploadReceipts(t, h, [][2]string{{"handy.pdf", "pdf"}}, map[string][]byte{"pdf": pdfNamed("handy")})
	waitRead(t, st)
	queued, _ := st.ImportQueue(t.Context(), store.ReceiptQueue)
	id := i(queued[0].ID)
	if !strings.Contains(get(t, h, "/receipts/review").Body.String(), `name="business_pct_`+id+`" min="1" max="100" step="1" inputmode="numeric" value="100"`) {
		t.Fatal("the review list has no business share field")
	}
	form := url.Values{"pick": {id}, "booked_on_" + id: {"03.08.2026"}, "amount_" + id: {"60,00"}, "vat_rate_" + id: {"20"},
		"direction_" + id: {"outgoing"}, "description_" + id: {"Handy August"}, "business_pct_" + id: {"0"}}
	if rec := post(t, h, "/receipts/review", form); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("0 %% accepted: %d", rec.Code)
	}
	form.Set("business_pct_"+id, "60")
	form.Set("business_note_"+id, "Einzelverbindungsnachweis")
	if rec := post(t, h, "/receipts/review", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("book = %d %s", rec.Code, rec.Body.String())
	}
	if e, _ := st.ListEntries(t.Context(), 1); e[0].BusinessPct != 60 || e[0].BusinessNote != "Einzelverbindungsnachweis" {
		t.Errorf("booked Beleg: %d %% %q", e[0].BusinessPct, e[0].BusinessNote)
	}

	importThroughUI(t, h, "Datum;Betrag;Empfänger;Zweck\n05.08.2026;-30,00;Magenta;Internet August\n")
	txs, _ := st.ListBankTransactions(t.Context(), true, 10)
	tx := i(txs[0].ID)
	if !strings.Contains(get(t, h, "/bank/drafts").Body.String(), `name="bpct_`+tx+`"`) {
		t.Fatal("the bank drafts have no business share field")
	}
	if rec := post(t, h, "/bank/drafts", url.Values{"pick": {tx}, "desc_" + tx: {"Internet"}, "vat_" + tx: {"20"},
		"bpct_" + tx: {"50"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("book draft = %d %s", rec.Code, rec.Body.String())
	}
	if e, _ := st.ListEntries(t.Context(), 1); e[0].BusinessPct != 50 {
		t.Errorf("booked draft share = %d", e[0].BusinessPct)
	}
}

// ADR 0045: the booking page changes the business share and shows the history.
func TestChangeShareOnBookingPage(t *testing.T) {
	h, _ := newTestServer(t)
	rec := post(t, h, "/entries", url.Values{"booked_on": {"2026-03-01"}, "direction": {"outgoing"},
		"description": {"Internet"}, "amount": {"30,00"}, "vat_rate": {"20"}})
	loc := rec.Header().Get("Location")
	if !strings.Contains(get(t, h, loc).Body.String(), "Betrieblichen Anteil ändern") {
		t.Fatal("no way to change the share")
	}
	if rec := post(t, h, loc+"/share", url.Values{"business_pct": {"150"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("150 %% = %d", rec.Code)
	}
	if rec := post(t, h, loc+"/share", url.Values{"business_pct": {"50"}, "business_note": {"halbe Nutzung"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("change = %d %s", rec.Code, rec.Body.String())
	}
	page := get(t, h, loc).Body.String()
	for _, want := range []string{"gebucht mit</span> 100 %", "geändert auf</span> 50 %", "halbe Nutzung", "50 % betrieblich, 50 % privat"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}
