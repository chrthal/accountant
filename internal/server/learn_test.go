package server_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/store"
)

// ADR 0044: a bank line similar to an earlier booking, by its bank text or
// by the booking's description, gets that booking's category and contact.
func TestDraftsFromSimilarBooking(t *testing.T) {
	h, st := newTestServer(t)
	food, _ := st.CreateCategory(t.Context(), store.Category{Name: "Lebensmittel", Direction: "outgoing"})
	hosting, _ := st.CreateCategory(t.Context(), store.Category{Name: "Hosting", Direction: "outgoing"})
	hetzner, _ := st.CreateContact(t.Context(), store.Contact{Kind: "vendor", Name: "Hetzner Online GmbH"})

	importThroughUI(t, h, "Datum;Betrag;Empfänger;Zweck\n02.08.2026;-12,40;BILLA DANKT 1234 WIEN;Kartenzahlung\n")
	txs, _ := st.ListBankTransactions(t.Context(), true, 10)
	post(t, h, "/entries", url.Values{"booked_on": {"2026-08-02"}, "direction": {"outgoing"}, "description": {"Kaffee"},
		"amount": {"12,40"}, "vat_rate": {"10"}, "category_id": {i(food)}, "bank_tx_id": {i(txs[0].ID)}})
	// Booked by hand, without a bank line: only its contact names it.
	post(t, h, "/entries", url.Values{"booked_on": {"2026-08-05"}, "direction": {"outgoing"}, "description": {"Server August"},
		"amount": {"40,00"}, "vat_rate": {"20"}, "category_id": {i(hosting)}, "contact_id": {i(hetzner)}})

	importThroughUI(t, h, "Datum;Betrag;Empfänger;Zweck\n"+
		"02.09.2026;-8,10;BILLA DANKT 5678 GRAZ;Kartenzahlung\n"+
		"05.09.2026;-40,00;HETZNER ONLINE;Rechnung 0815\n"+
		"06.09.2026;-15,00;Spar;Einkauf\n")
	txs, _ = st.ListBankTransactions(t.Context(), true, 10)
	ids := map[string]string{}
	for _, tx := range txs {
		ids[tx.Counterparty] = i(tx.ID)
	}

	body := get(t, h, "/bank/drafts").Body.String()
	for name, cat := range map[string]int64{"BILLA DANKT 5678 GRAZ": food, "HETZNER ONLINE": hosting} {
		id := ids[name]
		if !strings.Contains(body, `<select name="cat_`+id+`">`) {
			t.Fatalf("no draft for %s", name)
		}
		row := body[strings.Index(body, `<select name="cat_`+id+`">`):]
		row = row[:strings.Index(row, "</select>")]
		if !strings.Contains(row, `<option value="`+i(cat)+`" selected>`) {
			t.Errorf("%s: category not taken from the similar booking", name)
		}
	}
	if !strings.Contains(body, "ähnliche Buchung") || !strings.Contains(body, `name="desc_`+ids["BILLA DANKT 5678 GRAZ"]+`" value="BILLA DANKT 5678 GRAZ"`) {
		t.Error("a similar booking must be named, and the bank text kept")
	}
	if !strings.Contains(body, `<option value="`+i(hetzner)+`" selected>`) {
		t.Error("the contact of the similar booking is not proposed")
	}
	spar := body[strings.Index(body, `<select name="cat_`+ids["Spar"]+`">`):]
	if strings.Contains(spar[:strings.Index(spar, "</select>")], "selected") {
		t.Error("an unknown shop got a category")
	}

	form := get(t, h, "/entries/new?bank_tx="+ids["HETZNER ONLINE"]).Body.String()
	if !strings.Contains(form, "Vorausgefüllt ähnlich wie Buchung #2") {
		t.Error("the booking form from a bank line ignores the similar booking")
	}
}
