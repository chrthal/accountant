package store_test

import (
	"context"
	"testing"

	"github.com/chrthal/accountant/internal/bankcsv"
	"github.com/chrthal/accountant/internal/store"
)

const bankCSV = `Datum;Betrag;Empfänger;Zweck
15.03.2026;-1.234,56;Büro Müller GmbH;Rechnung 42
16.03.2026;2.000,00;Muster GmbH;Honorar März
`

func importSample(t *testing.T, st *store.Store) store.ImportResult {
	t.Helper()
	f, err := bankcsv.Parse([]byte(bankCSV))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rows, skipped, err := f.MapRows(bankcsv.Mapping{Date: 0, Amount: 1, Counterparty: 2, Purpose: 3, HasHeader: true})
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	res, err := st.ImportBankRows(context.Background(), rows, "umsaetze.csv", skipped)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return res
}

// Re-importing an overlapping export is normal practice and must not double
// anything (ADR 0009).
func TestImportIsIdempotent(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		first := importSample(t, st)
		if first.Imported != 2 || first.Duplicates != 0 {
			t.Errorf("first import = %+v, want 2 imported", first)
		}

		second := importSample(t, st)
		if second.Imported != 0 || second.Duplicates != 2 {
			t.Errorf("second import = %+v, want 2 duplicates and nothing imported", second)
		}

		list, err := st.ListBankTransactions(context.Background(), false, 100)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(list) != 2 {
			t.Errorf("got %d transactions after two imports, want 2", len(list))
		}
	})
}

func TestImportKeepsSignAndDirection(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		importSample(t, st)
		list, _ := st.ListBankTransactions(context.Background(), false, 100)

		byPurpose := map[string]store.BankTransaction{}
		for _, b := range list {
			byPurpose[b.Purpose] = b
		}
		out := byPurpose["Rechnung 42"]
		in := byPurpose["Honorar März"]

		if out.AmountCents != -123456 || out.Direction() != "outgoing" || out.AbsAmountCents() != 123456 {
			t.Errorf("outgoing transaction = %+v", out)
		}
		if in.AmountCents != 200000 || in.Direction() != "income" {
			t.Errorf("incoming transaction = %+v", in)
		}
	})
}

func TestMatchAndUnmatch(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		importSample(t, st)

		entryID, err := st.CreateEntry(ctx, store.NewEntry{
			BookedOn: "2026-03-15", Direction: "outgoing", Description: "Büromaterial",
			AmountGrossCents: 123456, VATRate: 20, Konto: "7600", Gegenkonto: "2800",
		})
		if err != nil {
			t.Fatalf("create entry: %v", err)
		}

		list, _ := st.ListBankTransactions(ctx, true, 100)
		var txID int64
		for _, b := range list {
			if b.AmountCents == -123456 {
				txID = b.ID
			}
		}
		if txID == 0 {
			t.Fatal("the outgoing transaction is not in the unmatched list")
		}

		if err := st.MatchBankTransaction(ctx, txID, entryID); err != nil {
			t.Fatalf("match: %v", err)
		}

		matched, err := st.GetBankTransaction(ctx, txID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if !matched.IsMatched() || matched.Matches[0].ID != entryID {
			t.Error("transaction is not linked to the entry")
		}
		if matched.MatchedEntryDescription() != "Büromaterial" {
			t.Errorf("matched description = %q, want the entry's", matched.MatchedEntryDescription())
		}

		// It drops out of both working lists once matched.
		unmatchedTx, _ := st.ListBankTransactions(ctx, true, 100)
		for _, b := range unmatchedTx {
			if b.ID == txID {
				t.Error("matched transaction still appears as unmatched")
			}
		}
		candidates, _ := st.UnmatchedEntries(ctx, 100)
		for _, e := range candidates {
			if e.ID == entryID {
				t.Error("matched entry still offered as a matching candidate")
			}
		}

		// Matching either side twice would count a payment twice.
		if err := st.MatchBankTransaction(ctx, txID, entryID); err == nil {
			t.Error("re-matching an already matched transaction succeeded")
		}

		if err := st.UnmatchBankTransaction(ctx, txID); err != nil {
			t.Fatalf("unmatch: %v", err)
		}
		again, _ := st.GetBankTransaction(ctx, txID)
		if again.IsMatched() {
			t.Error("transaction is still matched after unmatching")
		}
		// The entry itself is untouched by matching or unmatching.
		if _, err := st.GetEntry(ctx, entryID); err != nil {
			t.Errorf("entry disappeared: %v", err)
		}
	})
}

// A mismatch of amount or direction is a mistake worth refusing: silently
// linking them would hide the error instead of surfacing it.
func TestMatchRefusesMismatchedAmountOrDirection(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		importSample(t, st)
		list, _ := st.ListBankTransactions(ctx, true, 100)

		var outgoingTx int64
		for _, b := range list {
			if b.AmountCents == -123456 {
				outgoingTx = b.ID
			}
		}

		wrongAmount, _ := st.CreateEntry(ctx, store.NewEntry{
			BookedOn: "2026-03-15", Direction: "outgoing", Description: "Anderer Betrag",
			AmountGrossCents: 5000, VATRate: 20, Konto: "7600", Gegenkonto: "2800",
		})
		if err := st.MatchBankTransaction(ctx, outgoingTx, wrongAmount); err == nil {
			t.Error("matched a transaction to a booking with a different amount")
		}

		wrongDirection, _ := st.CreateEntry(ctx, store.NewEntry{
			BookedOn: "2026-03-15", Direction: "income", Description: "Falsche Richtung",
			AmountGrossCents: 123456, VATRate: 20, Konto: "2000", Gegenkonto: "4000",
		})
		if err := st.MatchBankTransaction(ctx, outgoingTx, wrongDirection); err == nil {
			t.Error("matched an outgoing transaction to an income booking")
		}

		if err := st.MatchBankTransaction(ctx, 999999, wrongAmount); err == nil {
			t.Error("matched a transaction that does not exist")
		}
		if err := st.UnmatchBankTransaction(ctx, 999999); err == nil {
			t.Error("unmatched a transaction that does not exist")
		}
	})
}

// ADR 0014: one incoming transfer pays a mixed-rate invoice, which ADR 0015
// books as two entries. The transaction matches both, and only their sum.
func TestOneTransactionMatchesSeveralEntries(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		contact := setupIssuer(t, st, false)
		inv, err := st.CreateInvoice(ctx, store.NewInvoice{
			ContactID: contact, IssueDate: "2026-08-18", ServiceDate: "2026-08-18",
			Lines: []store.InvoiceLine{
				{Description: "Beratung", QuantityMilli: 1000, UnitPriceCents: 10000, VATRate: 20},
				{Description: "Buch", QuantityMilli: 1000, UnitPriceCents: 5000, VATRate: 10},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.ImportBankRows(ctx, []bankcsv.Row{
			{BookedOn: "2026-08-25", AmountCents: inv.GrossCents, Counterparty: "Muster GmbH", Hash: "pay"},
		}, "t.csv", nil); err != nil {
			t.Fatal(err)
		}
		txs, _ := st.ListBankTransactions(ctx, true, 10)
		txID := txs[0].ID

		if got := store.SuggestInvoices(txs[0], mustOpen(t, st)); len(got) != 1 || got[0].ID != inv.ID {
			t.Errorf("invoice suggestions = %v, want the invoice", got)
		}

		ids, err := st.BookInvoicePayment(ctx, store.InvoicePayment{InvoiceID: inv.ID, PaidOn: "2026-08-25"})
		if err != nil || len(ids) != 2 {
			t.Fatalf("payment: %v %v", ids, err)
		}
		if err := st.MatchBankTransaction(ctx, txID, ids[0]); err == nil {
			t.Error("matching one of two payment entries was accepted — the amounts differ")
		}
		if err := st.MatchBankTransaction(ctx, txID, ids[0], ids[0]); err == nil {
			t.Error("the same entry listed twice was accepted")
		}
		if err := st.MatchBankTransaction(ctx, txID, ids...); err != nil {
			t.Fatalf("match both: %v", err)
		}
		tx, _ := st.GetBankTransaction(ctx, txID)
		if len(tx.Matches) != 2 || tx.MatchedEntryDescription() != "Rechnung "+inv.Number+" (2 Buchungen)" {
			t.Errorf("matches = %d, description %q", len(tx.Matches), tx.MatchedEntryDescription())
		}
		for _, id := range ids {
			linked, _ := st.BankTransactionsForEntry(ctx, id)
			if len(linked) != 1 || linked[0].ID != txID {
				t.Errorf("entry #%d does not know its transaction", id)
			}
		}
		if n, _ := st.CountUnmatchedBank(ctx); n != 0 {
			t.Errorf("unmatched = %d, want 0", n)
		}
		if err := st.UnmatchBankTransaction(ctx, txID); err != nil {
			t.Fatal(err)
		}
		if n, _ := st.CountUnmatchedBank(ctx); n != 1 {
			t.Errorf("after unmatch: unmatched = %d, want 1", n)
		}
	})
}

func mustOpen(t *testing.T, st *store.Store) []store.Invoice {
	t.Helper()
	open, err := st.OpenInvoices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return open
}

func TestSuggestMatchesPrefersTheNearestDate(t *testing.T) {
	tx := store.BankTransaction{BookedOn: "2026-03-10", AmountCents: -5000}
	cands := []store.Entry{
		{ID: 1, BookedOn: "2026-01-01", Direction: "outgoing", AmountGrossCents: 5000},
		{ID: 2, BookedOn: "2026-03-09", Direction: "outgoing", AmountGrossCents: 5000},
		{ID: 3, BookedOn: "2026-03-10", Direction: "income", AmountGrossCents: 5000},
		{ID: 4, BookedOn: "2026-03-10", Direction: "outgoing", AmountGrossCents: 5001},
	}
	got := store.SuggestMatches(tx, cands)
	if len(got) != 2 || got[0].ID != 2 || got[1].ID != 1 {
		t.Errorf("suggestions = %+v, want #2 then #1", got)
	}
}

func TestBankMappingIsRemembered(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if _, ok, _ := st.SavedBankMapping(ctx, "abc"); ok {
			t.Fatal("a mapping exists before any was saved")
		}
		m := bankcsv.Mapping{Date: 0, Amount: 3, Counterparty: -1, Purpose: 1}
		if err := st.SaveBankMapping(ctx, "abc", m); err != nil {
			t.Fatal(err)
		}
		m.Purpose = 2
		if err := st.SaveBankMapping(ctx, "abc", m); err != nil {
			t.Fatal(err)
		}
		got, ok, err := st.SavedBankMapping(ctx, "abc")
		if err != nil || !ok || got != m {
			t.Errorf("saved mapping = %+v %v %v, want %+v", got, ok, err, m)
		}
		// Bank mappings live in settings, and must not leak into Settings().
		if _, err := st.Settings(ctx); err != nil {
			t.Errorf("settings with a bank mapping stored: %v", err)
		}
	})
}

func TestLabelledTextIsSplitOnImportAndBackfilled(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		raw := "Empfänger: Hosting AG IBAN Empfänger: AT001234 Verwendungszweck: Server August"
		if _, err := st.ImportBankRows(ctx, []bankcsv.Row{
			{BookedOn: "2026-08-03", AmountCents: -54000, Counterparty: "Hosting AG", CounterpartyIBAN: "AT001234",
				Purpose: "Server August", RawText: raw, Hash: "new"},
			// Imported before the split existed: everything still in purpose.
			{BookedOn: "2026-07-03", AmountCents: -54000, Purpose: raw, Hash: "old"},
			{BookedOn: "2026-07-04", AmountCents: -5994, Purpose: "Kartenzahlung Billa 1010 Wien", Hash: "card"},
		}, "elba.csv", nil); err != nil {
			t.Fatal(err)
		}
		n, err := st.BackfillCounterparties(ctx)
		if err != nil || n != 1 {
			t.Fatalf("backfill = %d, %v; want exactly the one old labelled row", n, err)
		}
		if again, _ := st.BackfillCounterparties(ctx); again != 0 {
			t.Errorf("a second backfill changed %d rows", again)
		}
		txs, _ := st.ListBankTransactions(ctx, false, 10)
		for _, tx := range txs {
			switch tx.RowHash {
			case "new", "old":
				if tx.Counterparty != "Hosting AG" || tx.CounterpartyIBAN != "AT001234" ||
					tx.Purpose != "Server August" || tx.RawText != raw {
					t.Errorf("%s = %+v", tx.RowHash, tx)
				}
			case "card":
				if tx.Counterparty != "" || tx.RawText != "" {
					t.Errorf("unlabelled card payment was changed: %+v", tx)
				}
			}
		}
	})
}

func TestPreviousBookingIsFoundByIBANOrName(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		cat, _ := st.CreateCategory(ctx, store.Category{Name: "Miete", Direction: "outgoing"})
		st.ImportBankRows(ctx, []bankcsv.Row{
			{BookedOn: "2026-08-01", AmountCents: -80000, Counterparty: "Hausverwaltung Huber", CounterpartyIBAN: "AT11", Hash: "aug"},
			{BookedOn: "2026-09-01", AmountCents: -80000, Counterparty: "HV Huber (neuer Name)", CounterpartyIBAN: "AT11", Hash: "sep"},
			{BookedOn: "2026-09-02", AmountCents: -1500, Counterparty: "hausverwaltung huber", Hash: "fee"},
			{BookedOn: "2026-09-03", AmountCents: -999, Counterparty: "Unbekannt GmbH", Hash: "new"},
		}, "elba.csv", nil)
		txs, _ := st.ListBankTransactions(ctx, true, 10)
		byHash := map[string]store.BankTransaction{}
		for _, tx := range txs {
			byHash[tx.RowHash] = tx
		}

		n := sampleEntry()
		n.BookedOn, n.Description, n.AmountGrossCents, n.VATRate, n.CategoryID = "2026-08-01", "Miete August", 80000, 0, &cat
		aug, _ := st.CreateEntry(ctx, n)
		if err := st.MatchBankTransaction(ctx, byHash["aug"].ID, aug); err != nil {
			t.Fatal(err)
		}

		for hash, wantFound := range map[string]bool{"sep": true, "fee": true, "new": false} {
			prev, ok, err := st.PreviousBooking(ctx, byHash[hash])
			if err != nil || ok != wantFound {
				t.Errorf("%s: found=%v err=%v, want %v", hash, ok, err, wantFound)
				continue
			}
			if ok && (prev.ID != aug || prev.CategoryName != "Miete" || prev.VATRate != 0) {
				t.Errorf("%s: previous = %+v", hash, prev)
			}
		}

		// A reversed booking is no template.
		st.ReverseEntry(ctx, aug, "2026-08-02")
		if _, ok, _ := st.PreviousBooking(ctx, byHash["sep"]); ok {
			t.Error("a reversed booking was offered as the template")
		}
	})
}
