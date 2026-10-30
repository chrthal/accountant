package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chrthal/accountant/internal/llm"
	"github.com/chrthal/accountant/internal/server"
	"github.com/chrthal/accountant/internal/store"
)

// A bank export with a recurring payee (the rent, seen in August and
// September), a new payee, and a customer payment for an invoice.
const draftsExport = "Datum;Betrag;Empfänger;Zweck\n" +
	"01.08.2026;-800,00;Hausverwaltung Huber;Miete August\n" +
	"01.09.2026;-800,00;Hausverwaltung Huber;Miete September\n" +
	"03.09.2026;-49,90;T-Mobile Austria GmbH;Rechnung 09/2026\n"

func TestDraftsReuseTheLastBookingAndBookOnConfirm(t *testing.T) {
	h, st := newTestServer(t)
	cat, _ := st.CreateCategory(t.Context(), store.Category{Name: "Miete", Direction: "outgoing"})
	importThroughUI(t, h, draftsExport)
	txs, _ := st.ListBankTransactions(t.Context(), true, 10)
	byDate := map[string]store.BankTransaction{}
	for _, tx := range txs {
		byDate[tx.BookedOn] = tx
	}

	// August's rent is booked by hand once, with the bank line.
	aug := byDate["2026-08-01"]
	post(t, h, "/entries", url.Values{
		"booked_on": {"2026-08-01"}, "direction": {"outgoing"}, "description": {"Miete Büro"},
		"amount": {"800,00"}, "vat_rate": {"0"}, "category_id": {i(cat)}, "bank_tx_id": {i(aug.ID)},
	})

	body := get(t, h, "/bank/drafts").Body.String()
	sep, tmobile := byDate["2026-09-01"], byDate["2026-09-03"]
	if !strings.Contains(body, `name="desc_`+i(sep.ID)+`" value="Miete Büro"`) || !strings.Contains(body, "wie zuletzt") {
		t.Fatal("September's rent is not proposed like August's")
	}
	if !strings.Contains(body, `name="pick" value="`+i(sep.ID)+`" checked`) {
		t.Error("the 'wie zuletzt' draft is not preselected")
	}
	if strings.Contains(body, `name="pick" value="`+i(tmobile.ID)+`" checked`) {
		t.Error("a draft without any proposal is preselected")
	}
	// The form from a bank line starts from the same booking.
	if form := get(t, h, "/entries/new?bank_tx="+i(sep.ID)).Body.String(); !strings.Contains(form, "Vorausgefüllt wie Buchung #1") {
		t.Error("the booking form from a bank line ignores the earlier booking")
	}

	// Confirm only the rent, as the user left it.
	rec := post(t, h, "/bank/drafts", url.Values{
		"pick":                  {i(sep.ID)},
		"desc_" + i(sep.ID):     {"Miete September"},
		"cat_" + i(sep.ID):      {i(cat)},
		"vat_" + i(sep.ID):      {"0"},
		"desc_" + i(tmobile.ID): {"ignored"}, "vat_" + i(tmobile.ID): {"20"},
	})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/bank/drafts?booked=1" {
		t.Fatalf("book = %d → %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	got, _ := st.GetBankTransaction(t.Context(), sep.ID)
	if !got.IsMatched() || got.Matches[0].Description != "Miete September" || got.Matches[0].AmountGrossCents != 80000 ||
		got.Matches[0].CategoryName != "Miete" || got.Matches[0].BookedOn != "2026-09-01" {
		t.Errorf("booked = %+v", got.Matches)
	}
	if other, _ := st.GetBankTransaction(t.Context(), tmobile.ID); other.IsMatched() {
		t.Error("an unticked draft was booked")
	}
}

func TestDraftsUnderKleinunternehmer(t *testing.T) {
	h, st := newTestServer(t)
	configureIssuer(t, h, true) // Kleinunternehmer
	contactID := createContact(t, h, st)
	post(t, h, "/invoices", invoiceForm(contactID, [5]string{"Beratung", "1", "Std.", "100,00", "0"}))
	importThroughUI(t, h, "Datum;Betrag;Empfänger;Zweck\n20.08.2026;100,00;Muster GmbH;RE 2026-0001\n"+
		"03.09.2026;-49,90;T-Mobile;Rechnung\n05.09.2026;12,00;Zinsen Bank;Habenzinsen\n")
	body := get(t, h, "/bank/drafts").Body.String()
	if strings.Contains(body, "Muster GmbH</strong>") {
		t.Error("an invoice payment appears among the drafts")
	}
	txs, _ := st.ListBankTransactions(t.Context(), true, 10)
	for _, tx := range txs {
		start := strings.Index(body, `name="pick" value="`+i(tx.ID)+`"`)
		if start < 0 {
			continue
		}
		row := body[start : start+strings.Index(body[start:], "</section>")]
		switch tx.BookedOn {
		case "2026-09-03": // expense: the receipt's rate is kept (ADR 0022)
			if !strings.Contains(row, `<option value="20" selected>`) {
				t.Errorf("the expense lost its receipt VAT rate:\n%s", row)
			}
		case "2026-09-05": // income: no VAT under the exemption
			if !strings.Contains(row, `<option value="0" selected>`) {
				t.Errorf("Kleinunternehmer income is not at 0 %%:\n%s", row)
			}
		}
	}
}

// ADR 0022: under the exemption the VAT on expenses is recorded but nowhere
// counted as Vorsteuer, and switching the exemption off counts from its end
// date on, never retroactively.
func TestKleinunternehmerPeriodCountsVATCorrectly(t *testing.T) {
	h, _ := newTestServer(t)
	configureIssuer(t, h, true)
	book(t, h, "2026-03-15", "outgoing", "Laptop", "1.200,00")                // 200,00 VAT, exempt period
	book(t, h, "2026-03-16", "income", "Beratung", "500,00", "vat_rate", "0") // no VAT charged

	overview := get(t, h, "/?year=2026").Body.String()
	if strings.Contains(overview, "USt-Saldo") || !strings.Contains(overview, "keine Zahllast, kein Vorsteuerabzug") {
		t.Error("the overview shows a VAT saldo for a Kleinunternehmer year")
	}
	if vat := get(t, h, "/reports/vat?year=2026").Body.String(); strings.Contains(vat, "Gutschrift 2026") ||
		!strings.Contains(vat, "keine Zahllast und keine Gutschrift") {
		t.Error("the VAT page shows a Gutschrift for a Kleinunternehmer year")
	}
	if entry := get(t, h, "/entries/1").Body.String(); !strings.Contains(entry, "USt (nicht abziehbar, Kleinunternehmer)") {
		t.Error("the booking page calls it Vorsteuer")
	}

	// Switching off without saying when it ended is refused: it would make
	// the laptop's VAT deductible after the fact.
	if rec := post(t, h, "/settings", url.Values{"issuer_name": {"Mustermann IT"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("switch-off without an end date = %d, want 422", rec.Code)
	}
	if rec := post(t, h, "/settings", url.Values{"issuer_name": {"Mustermann IT"},
		"kleinunternehmer_until": {"2026-03-31"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("switch-off with an end date = %d: %s", rec.Code, rec.Body.String())
	}
	book(t, h, "2026-04-10", "outgoing", "Monitor", "240,00") // 40,00 VAT, normal period

	overview = get(t, h, "/?year=2026").Body.String()
	for _, want := range []string{"USt-Saldo", "davon Vorsteuer 40,00", "USt nicht abziehbar 200,00",
		"nur außerhalb der Kleinunternehmerzeit", "Gutschrift"} {
		if !strings.Contains(overview, want) {
			t.Errorf("mixed year: overview lacks %q", want)
		}
	}
	if !strings.Contains(overview, "−40,00") && !strings.Contains(overview, "-40,00") {
		t.Error("the year's saldo is not the April Vorsteuer only (−40,00)")
	}
	vat := get(t, h, "/reports/vat?year=2026").Body.String()
	if !strings.Contains(vat, "zeitweise (bis 31.03.2026)") || !strings.Contains(vat, "USt n. abz.") {
		t.Error("the VAT page does not show the partial exemption")
	}
	csv := get(t, h, "/export/csv?year=2026").Body.String()
	if !strings.Contains(csv, "nein (Kleinunternehmer)") || !strings.Contains(csv, ";ja") ||
		!strings.Contains(csv, "Ausgaben, Kleinunternehmer (USt nicht abziehbar)") {
		t.Error("the CSV export does not separate deductible from exempt-period VAT")
	}
	if !strings.Contains(get(t, h, "/entries/1").Body.String(), "nicht abziehbar") ||
		!strings.Contains(get(t, h, "/entries/3").Body.String(), "<dt>Vorsteuer</dt>") {
		t.Error("the booking pages do not follow their own date")
	}
}

// The model is asked only about lines the rules leave empty, its answer is
// validated, and it is shown as a suggestion, never booked by itself.
func TestLLMSuggestsOnlyWhereTheRulesHaveNothing(t *testing.T) {
	var calls atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if strings.Contains(req.Messages[1].Content, "Miete September") {
			t.Error("the model was asked about a line the rules already cover")
		}
		// Category 2 is "Telefon"; contact 99 does not exist.
		json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{
			"content": `{"description":"Mobilfunk September","category_id":2,"contact_id":99,"vat_rate":"20","confidence":"medium"}`}})
	}))
	defer fake.Close()

	st, err := store.Open("sqlite://" + filepath.Join(t.TempDir(), "llm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := server.New(st, "test", server.WithLLM(llm.New(fake.URL, "fake-model")))
	miete, _ := st.CreateCategory(t.Context(), store.Category{Name: "Miete", Direction: "outgoing"})
	st.CreateCategory(t.Context(), store.Category{Name: "Telefon", Direction: "outgoing"})

	// August arrives and is booked by hand first. Only then does the rest
	// arrive, so the rule for the rent exists when the worker runs.
	importThroughUI(t, h, "Datum;Betrag;Empfänger;Zweck\n01.08.2026;-800,00;Hausverwaltung Huber;Miete August\n")
	waitIdle(t, h)
	txs, _ := st.ListBankTransactions(t.Context(), true, 10)
	post(t, h, "/entries", url.Values{"booked_on": {"2026-08-01"}, "direction": {"outgoing"},
		"description": {"Miete Büro"}, "amount": {"800,00"}, "vat_rate": {"0"},
		"category_id": {i(miete)}, "bank_tx_id": {i(txs[0].ID)}})
	calls.Store(0)
	importThroughUI(t, h, draftsExport) // starts the suggestion worker

	var body string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		post(t, h, "/bank/drafts/suggest", nil)
		body = get(t, h, "/bank/drafts").Body.String()
		if strings.Contains(body, "KI-Vorschlag") && !strings.Contains(body, "werden erstellt") {
			break
		}
	}
	if !strings.Contains(body, `value="Mobilfunk September"`) || !strings.Contains(body, "KI-Vorschlag") {
		t.Fatalf("no model suggestion shown:\n%s", body)
	}
	if !strings.Contains(body, `<option value="2" selected>Telefon</option>`) {
		t.Error("the validated category is not preselected")
	}
	if strings.Contains(body, `value="99" selected`) {
		t.Error("an invented contact got through")
	}
	if n := calls.Load(); n < 1 {
		t.Errorf("the model was called %d times", n)
	}
	if entries, _ := st.ListEntries(t.Context(), 10); len(entries) != 1 {
		t.Errorf("%d bookings exist — the model must never book by itself", len(entries))
	}
}

func i(n int64) string { return strconv.FormatInt(n, 10) }

// waitIdle waits until the background suggestion worker has finished.
func waitIdle(t *testing.T, h http.Handler) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if !strings.Contains(get(t, h, "/bank/drafts").Body.String(), "werden erstellt") {
			return
		}
	}
	t.Fatal("the suggestion worker did not finish")
}

// ADR 0021: keywords assign the category, the category's default VAT rate
// applies, and for an expense the model's VAT guess is never used.
func TestCategoryKeywordsAndDefaultVATInDrafts(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{
			"content": `{"description":"Irgendwas","category_id":0,"contact_id":0,"vat_rate":"13","confidence":"high"}`}})
	}))
	defer fake.Close()
	st, err := store.Open("sqlite://" + filepath.Join(t.TempDir(), "cat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := server.New(st, "test", server.WithLLM(llm.New(fake.URL, "fake-model")))

	post(t, h, "/settings", url.Values{"issuer_name": {"Muster IT"}, "default_vat_outgoing": {"20"}})
	if rec := post(t, h, "/categories", url.Values{"name": {"Lebensmittel & Bewirtung"}, "direction": {"outgoing"},
		"keywords": {"billa, spar"}, "default_vat_rate": {"10"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("create category = %d: %s", rec.Code, rec.Body.String())
	}
	post(t, h, "/categories", url.Values{"name": {"Miete"}, "direction": {"outgoing"}})

	importThroughUI(t, h, "Datum;Betrag;Empfänger;Zweck\n"+
		"14.09.2026;-23,47;;Kartenzahlung BILLA DANKT 1010 WIEN\n"+
		"01.09.2026;-850,00;Hausverwaltung;Miete September\n"+
		"20.09.2026;-99,00;Unbekannt GmbH;Lieferung\n")
	waitIdle(t, h)
	body := get(t, h, "/bank/drafts").Body.String()

	txs, _ := st.ListBankTransactions(t.Context(), true, 10)
	row := map[string]string{}
	for _, tx := range txs {
		start := strings.Index(body, `name="pick" value="`+i(tx.ID)+`"`)
		end := strings.Index(body[start:], "</section>")
		row[tx.BookedOn] = body[start : start+end]
	}

	billa := row["2026-09-14"]
	if !strings.Contains(billa, "Kategorie erkannt") || !strings.Contains(billa, `selected>Lebensmittel &amp; Bewirtung`) ||
		!strings.Contains(billa, `<option value="10" selected>`) || !strings.Contains(billa, `" checked>`) {
		t.Errorf("BILLA row: want keyword hit, category VAT 10 %%, preselected:\n%s", billa)
	}
	miete := row["2026-09-01"]
	if !strings.Contains(miete, "Kategorie erkannt") || !strings.Contains(miete, `selected>Miete`) ||
		!strings.Contains(miete, `<option value="20" selected>`) || !strings.Contains(miete, ">Standard<") {
		t.Errorf("rent row: want name hit and the standard 20 %%:\n%s", miete)
	}
	other := row["2026-09-20"]
	if !strings.Contains(other, "KI-Vorschlag") || strings.Contains(other, `<option value="13" selected>`) ||
		!strings.Contains(other, `<option value="20" selected>`) {
		t.Errorf("unknown payee: the model's 13 %% must not be used for an expense:\n%s", other)
	}

	// The settings form without the field keeps 20 %, rather than 0 %.
	post(t, h, "/settings", url.Values{"issuer_name": {"Muster IT"}})
	if s, _ := st.Settings(t.Context()); s.DefaultVATOutgoing != 20 {
		t.Errorf("default after a save without the field = %d", s.DefaultVATOutgoing)
	}
	// The booking form for an expense starts at the configured standard.
	post(t, h, "/settings", url.Values{"issuer_name": {"Muster IT"}, "default_vat_outgoing": {"10"}})
	if form := get(t, h, "/entries/new").Body.String(); !strings.Contains(form, `<option value="10" selected>`) {
		t.Error("the expense form does not start at the standard VAT rate")
	}
	// "Buchung anlegen" from the BILLA line applies the category rule too.
	for _, tx := range txs {
		if tx.BookedOn == "2026-09-14" {
			form := get(t, h, "/entries/new?bank_tx="+i(tx.ID)).Body.String()
			if !strings.Contains(form, "Kategorie erkannt") || !strings.Contains(form, `data-vat="10" selected`) {
				t.Error("the form from the BILLA line does not preselect the recognised category")
			}
		}
	}
}
