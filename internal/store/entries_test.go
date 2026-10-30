package store_test

import (
	"context"
	"testing"

	"github.com/chrthal/accountant/internal/store"
)

func openEach(t *testing.T, fn func(t *testing.T, st *store.Store)) {
	t.Helper()
	for engine, dsn := range dsns(t) {
		t.Run(engine, func(t *testing.T) {
			st, err := store.Open(dsn)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			t.Cleanup(func() { st.Close() })
			fn(t, st)
		})
	}
}

func sampleEntry() store.NewEntry {
	return store.NewEntry{
		BookedOn:         "2026-03-15",
		Direction:        "outgoing",
		Description:      "Büromaterial",
		AmountGrossCents: 12000,
		VATRate:          20,
		Konto:            "7600",
		Gegenkonto:       "2800",
	}
}

func TestCreateEntrySplitsVAT(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		id, err := st.CreateEntry(ctx, sampleEntry())
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		got, err := st.GetEntry(ctx, id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.VATCents != 2000 || got.NetCents != 10000 {
			t.Errorf("split = (%d, %d), want (2000, 10000)", got.VATCents, got.NetCents)
		}
		if got.VATCents+got.NetCents != got.AmountGrossCents {
			t.Error("stored parts do not sum to the stored gross")
		}
	})
}

func TestEntryValidation(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		bad := map[string]func(n *store.NewEntry){
			"bad date":       func(n *store.NewEntry) { n.BookedOn = "15.03.2026" },
			"bad direction":  func(n *store.NewEntry) { n.Direction = "sideways" },
			"zero amount":    func(n *store.NewEntry) { n.AmountGrossCents = 0 },
			"bad vat rate":   func(n *store.NewEntry) { n.VATRate = 7 },
			"no description": func(n *store.NewEntry) { n.Description = "" },
		}
		for name, mutate := range bad {
			n := sampleEntry()
			mutate(&n)
			if _, err := st.CreateEntry(ctx, n); err == nil {
				t.Errorf("%s: accepted, want an error", name)
			}
		}
	})
}

// Konto/Gegenkonto are optional on a booking (ADR 0012): cash-basis
// Einnahmen-Ausgaben-Rechnung does not need them, and only the DATEV export
// does — which checks for itself.
func TestEntryAccountsAreOptional(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		n := sampleEntry()
		n.Konto, n.Gegenkonto = "", ""
		id, err := st.CreateEntry(ctx, n)
		if err != nil {
			t.Fatalf("booking without account tags: %v", err)
		}
		got, err := st.GetEntry(ctx, id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Konto != "" || got.Gegenkonto != "" {
			t.Errorf("accounts = %q/%q, want both empty", got.Konto, got.Gegenkonto)
		}
	})
}

// A blank account is filled from the booking's category, and the resolved value
// is stored on the row rather than looked up on read (ADR 0012) — editing a
// category later must not move the numbers of a booking already exported.
func TestCategoryDefaultsFillBlankAccounts(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		catID, err := st.CreateCategory(ctx, store.Category{
			Name: "Bürobedarf", Direction: "outgoing",
			DefaultKonto: "7600", DefaultGegenkonto: "2700",
		})
		if err != nil {
			t.Fatalf("create category: %v", err)
		}

		// Blank accounts: the category supplies both.
		n := sampleEntry()
		n.Konto, n.Gegenkonto = "", ""
		n.CategoryID = &catID
		id, err := st.CreateEntry(ctx, n)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		got, _ := st.GetEntry(ctx, id)
		if got.Konto != "7600" || got.Gegenkonto != "2700" {
			t.Errorf("resolved accounts = %q/%q, want 7600/2700", got.Konto, got.Gegenkonto)
		}

		// An explicit value on the booking wins over the category's default.
		n = sampleEntry()
		n.Konto, n.Gegenkonto = "7610", ""
		n.CategoryID = &catID
		id, err = st.CreateEntry(ctx, n)
		if err != nil {
			t.Fatalf("create with override: %v", err)
		}
		got, _ = st.GetEntry(ctx, id)
		if got.Konto != "7610" || got.Gegenkonto != "2700" {
			t.Errorf("override = %q/%q, want 7610/2700", got.Konto, got.Gegenkonto)
		}

		// A category with no defaults configured leaves the booking blank
		// rather than inventing anything.
		emptyCat, err := st.CreateCategory(ctx, store.Category{Name: "Sonstiges", Direction: "both"})
		if err != nil {
			t.Fatalf("create category: %v", err)
		}
		n = sampleEntry()
		n.Konto, n.Gegenkonto = "", ""
		n.CategoryID = &emptyCat
		id, err = st.CreateEntry(ctx, n)
		if err != nil {
			t.Fatalf("create with an unconfigured category: %v", err)
		}
		got, _ = st.GetEntry(ctx, id)
		if got.Konto != "" || got.Gegenkonto != "" {
			t.Errorf("accounts = %q/%q, want both empty", got.Konto, got.Gegenkonto)
		}
	})
}

// The core guarantee of ADR 0002/0007: the ledger cannot be rewritten, and the
// database — not just the app — is what refuses.
func TestEntriesAreAppendOnlyInTheDatabase(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		id, err := st.CreateEntry(ctx, sampleEntry())
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		_, err = st.DB.ExecContext(ctx,
			st.Rebind("UPDATE entries SET description = ? WHERE id = ?"), "tampered", id)
		if err == nil {
			t.Error("UPDATE on entries succeeded — the ledger is not append-only")
		}

		_, err = st.DB.ExecContext(ctx, st.Rebind("DELETE FROM entries WHERE id = ?"), id)
		if err == nil {
			t.Error("DELETE on entries succeeded — the ledger is not append-only")
		}

		still, err := st.GetEntry(ctx, id)
		if err != nil {
			t.Fatalf("get after tamper attempts: %v", err)
		}
		if still.Description != "Büromaterial" {
			t.Errorf("description = %q, want it unchanged", still.Description)
		}
	})
}

func TestReverseEntry(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		id, err := st.CreateEntry(ctx, sampleEntry())
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		revID, err := st.ReverseEntry(ctx, id, "2026-03-20")
		if err != nil {
			t.Fatalf("reverse: %v", err)
		}

		orig, err := st.GetEntry(ctx, id)
		if err != nil {
			t.Fatalf("get original: %v", err)
		}
		rev, err := st.GetEntry(ctx, revID)
		if err != nil {
			t.Fatalf("get reversal: %v", err)
		}

		if !orig.IsReversed() || *orig.ReversedByID != revID {
			t.Error("original does not point at its correction")
		}
		if !rev.IsReversal() || *rev.ReversesEntryID != id {
			t.Error("correction does not point at the original")
		}
		if rev.Direction != "income" {
			t.Errorf("reversal direction = %q, want the opposite of outgoing", rev.Direction)
		}
		if rev.AmountGrossCents != orig.AmountGrossCents || rev.VATCents != orig.VATCents {
			t.Error("reversal amounts do not match the original")
		}
		if rev.Konto != orig.Gegenkonto || rev.Gegenkonto != orig.Konto {
			t.Error("reversal did not swap Konto and Gegenkonto")
		}
		if orig.Effective() || rev.Effective() {
			t.Error("a reversed pair must drop out of the effective set")
		}

		// A booking can only be corrected once; a second Storno would double it.
		if _, err := st.ReverseEntry(ctx, id, "2026-03-21"); err == nil {
			t.Error("second reversal succeeded, want an error")
		}
	})
}

func TestTotalsIgnoreReversedPairs(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		year := 2031

		keep := sampleEntry()
		keep.BookedOn, keep.Direction, keep.AmountGrossCents = "2031-02-01", "income", 55000
		if _, err := st.CreateEntry(ctx, keep); err != nil {
			t.Fatalf("create kept entry: %v", err)
		}

		drop := sampleEntry()
		drop.BookedOn, drop.Direction, drop.AmountGrossCents = "2031-02-02", "income", 99900
		dropID, err := st.CreateEntry(ctx, drop)
		if err != nil {
			t.Fatalf("create entry to reverse: %v", err)
		}
		if _, err := st.ReverseEntry(ctx, dropID, "2031-02-03"); err != nil {
			t.Fatalf("reverse: %v", err)
		}

		tot, err := st.Totals(ctx, year)
		if err != nil {
			t.Fatalf("totals: %v", err)
		}
		if tot.IncomeGrossCents != 55000 {
			t.Errorf("income = %d, want 55000 (the reversed pair must not count)", tot.IncomeGrossCents)
		}
		if tot.OutgoingGrossCents != 0 {
			t.Errorf("outgoing = %d, want 0 (a Storno is not an expense)", tot.OutgoingGrossCents)
		}
		if tot.ResultCents() != 55000 {
			t.Errorf("result = %d, want 55000", tot.ResultCents())
		}
	})
}

func TestContacts(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		id, err := st.CreateContact(ctx, store.Contact{
			Kind: "customer", Name: "Muster GmbH", UID: "ATU12345678",
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		list, err := st.ListContacts(ctx)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var found bool
		for _, c := range list {
			if c.ID == id && c.Name == "Muster GmbH" && c.UID == "ATU12345678" {
				found = true
			}
		}
		if !found {
			t.Error("created contact not in the list")
		}

		if _, err := st.CreateContact(ctx, store.Contact{Kind: "customer"}); err == nil {
			t.Error("nameless contact accepted, want an error")
		}
		if _, err := st.CreateContact(ctx, store.Contact{Kind: "alien", Name: "x"}); err == nil {
			t.Error("bad kind accepted, want an error")
		}
	})
}

func TestMonthlyTotalsMatchTotalsAndSkipReversedPairs(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		mk := func(date, dir string, cents int64) int64 {
			n := sampleEntry()
			n.BookedOn, n.Direction, n.AmountGrossCents = date, dir, cents
			id, err := st.CreateEntry(ctx, n)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			return id
		}
		mk("2026-01-10", "income", 100000)
		mk("2026-01-20", "outgoing", 12000)
		mk("2026-03-05", "income", 50000)
		wrong := mk("2026-03-06", "outgoing", 99900)
		mk("2025-12-31", "income", 777) // other year
		if _, err := st.ReverseEntry(ctx, wrong, "2026-04-01"); err != nil {
			t.Fatalf("reverse: %v", err)
		}

		months, err := st.MonthlyTotals(ctx, 2026)
		if err != nil {
			t.Fatalf("monthly: %v", err)
		}
		if months[0].IncomeGrossCents != 100000 || months[0].OutgoingGrossCents != 12000 {
			t.Errorf("January = %+v", months[0])
		}
		if months[2].IncomeGrossCents != 50000 || months[2].OutgoingGrossCents != 0 {
			t.Errorf("March = %+v — the reversed booking must not count", months[2])
		}
		if months[3].IncomeGrossCents != 0 {
			t.Errorf("April = %+v — the Storno must not count", months[3])
		}
		if months[11].Month != 12 {
			t.Errorf("months are not numbered 1–12: %+v", months[11])
		}

		totals, _ := st.Totals(ctx, 2026)
		var in, out int64
		for _, m := range months {
			in += m.IncomeGrossCents
			out += m.OutgoingGrossCents
		}
		if in != totals.IncomeGrossCents || out != totals.OutgoingGrossCents {
			t.Errorf("monthly sums %d/%d disagree with Totals %d/%d", in, out,
				totals.IncomeGrossCents, totals.OutgoingGrossCents)
		}
	})
}

func TestFindEntriesFiltersAndSumsTheWholeSet(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		cat, err := st.CreateCategory(ctx, store.Category{Name: "Reise", Direction: "outgoing"})
		if err != nil {
			t.Fatal(err)
		}
		cust, _ := st.CreateContact(ctx, store.Contact{Kind: "customer", Name: "Muster GmbH"})
		mk := func(date, dir, desc string, cents int64, category, contact *int64) int64 {
			n := sampleEntry()
			n.BookedOn, n.Direction, n.Description, n.AmountGrossCents = date, dir, desc, cents
			n.CategoryID, n.ContactID = category, contact
			id, err := st.CreateEntry(ctx, n)
			if err != nil {
				t.Fatal(err)
			}
			return id
		}
		mk("2026-02-01", "outgoing", "Zugticket Wien", 5000, &cat, nil)
		mk("2026-02-15", "income", "Beratung Februar", 120000, nil, &cust)
		mk("2026-03-01", "outgoing", "Hotel Graz", 20000, &cat, nil)
		wrong := mk("2026-03-02", "income", "Beratung doppelt", 120000, nil, &cust)
		mk("2025-12-01", "outgoing", "Zugticket alt", 4000, &cat, nil)
		if _, err := st.ReverseEntry(ctx, wrong, "2026-03-03"); err != nil {
			t.Fatal(err)
		}

		check := func(name string, f store.EntryFilter, wantTotal int, wantIn, wantOut int64) {
			t.Helper()
			p, err := st.FindEntries(ctx, f)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if p.Total != wantTotal || p.IncomeGrossCents != wantIn || p.OutgoingGrossCents != wantOut {
				t.Errorf("%s: total %d in %d out %d, want %d / %d / %d", name,
					p.Total, p.IncomeGrossCents, p.OutgoingGrossCents, wantTotal, wantIn, wantOut)
			}
			if len(p.Entries) > p.Total {
				t.Errorf("%s: page longer than the total", name)
			}
		}
		// 2026 has 5 rows (incl. the Storno pair), but only the effective ones sum.
		check("year", store.EntryFilter{Year: 2026}, 5, 120000, 25000)
		check("month", store.EntryFilter{Year: 2026, Month: 2}, 2, 120000, 5000)
		check("direction", store.EntryFilter{Year: 2026, Direction: "outgoing"}, 3, 0, 25000)
		check("category", store.EntryFilter{CategoryID: cat}, 3, 0, 29000)
		check("text", store.EntryFilter{Query: "zugticket"}, 2, 0, 9000)
		check("contact name", store.EntryFilter{Query: "MUSTER"}, 3, 120000, 0) // Storno keeps the contact
		check("wildcards are literal-free", store.EntryFilter{Query: "%"}, 6, 120000, 29000)

		p, _ := st.FindEntries(ctx, store.EntryFilter{Year: 2026, Limit: 2, Offset: 2})
		if len(p.Entries) != 2 || p.Total != 5 {
			t.Errorf("paging: got %d entries of %d", len(p.Entries), p.Total)
		}
		if p.Entries[0].CategoryName == "" && p.Entries[1].CategoryName == "" {
			t.Error("category names are not joined onto the list")
		}

		years, err := st.BookingYears(ctx)
		if err != nil || len(years) < 2 || years[len(years)-1] != 2025 {
			t.Errorf("years = %v, %v", years, err)
		}
	})
}

func TestContactsAndCategoriesAreEditable(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		cat, _ := st.CreateCategory(ctx, store.Category{Name: "Büro", Direction: "outgoing",
			DefaultKonto: "7600", DefaultGegenkonto: "2700"})
		n := sampleEntry()
		n.Konto, n.Gegenkonto, n.CategoryID = "", "", &cat
		booked, err := st.CreateEntry(ctx, n)
		if err != nil {
			t.Fatal(err)
		}

		if err := st.UpdateCategory(ctx, store.Category{ID: cat, Name: "Bürobedarf", Direction: "outgoing",
			DefaultKonto: "7610", DefaultGegenkonto: "2700"}); err != nil {
			t.Fatalf("update category: %v", err)
		}
		e, _ := st.GetEntry(ctx, booked)
		if e.Konto != "7600" {
			t.Errorf("editing the category moved an existing booking's Konto to %q", e.Konto)
		}
		if e.CategoryName != "Bürobedarf" {
			t.Errorf("category name = %q, want the new name", e.CategoryName)
		}
		if _, err := st.CreateCategory(ctx, store.Category{Name: "Bürobedarf", Direction: "both"}); err == nil {
			t.Error("a duplicate category name was accepted")
		}
		if err := st.UpdateCategory(ctx, store.Category{ID: 9999, Name: "x", Direction: "both"}); err != store.ErrNotFound {
			t.Errorf("update of a missing category = %v, want ErrNotFound", err)
		}

		id, _ := st.CreateContact(ctx, store.Contact{Kind: "customer", Name: "Alt"})
		if err := st.UpdateContact(ctx, store.Contact{ID: id, Kind: "both", Name: "Neu", UID: " ATU1 "}); err != nil {
			t.Fatalf("update contact: %v", err)
		}
		c, err := st.GetContact(ctx, id)
		if err != nil || c.Name != "Neu" || c.Kind != "both" || c.UID != "ATU1" {
			t.Errorf("contact after update = %+v, %v", c, err)
		}
		if err := st.UpdateContact(ctx, store.Contact{ID: id, Kind: "both"}); err == nil {
			t.Error("a contact without a name was accepted")
		}
		if _, err := st.GetContact(ctx, 9999); err != store.ErrNotFound {
			t.Errorf("missing contact = %v, want ErrNotFound", err)
		}
	})
}

func TestCategoryBreakdown(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		travel, _ := st.CreateCategory(ctx, store.Category{Name: "Reise", Direction: "outgoing"})
		mk := func(dir string, cents int64, cat *int64) int64 {
			n := sampleEntry()
			n.Direction, n.AmountGrossCents, n.CategoryID = dir, cents, cat
			id, err := st.CreateEntry(ctx, n)
			if err != nil {
				t.Fatal(err)
			}
			return id
		}
		mk("outgoing", 12000, &travel)
		wrong := mk("outgoing", 99999, &travel)
		mk("income", 240000, nil)
		st.ReverseEntry(ctx, wrong, "2026-03-20")

		rows, err := st.CategoryBreakdown(ctx, 2026)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 {
			t.Fatalf("rows = %+v, want the uncategorised row and Reise", rows)
		}
		if rows[0].ID != 0 || rows[0].IncomeGrossCents != 240000 || rows[0].IncomeNetCents != 200000 {
			t.Errorf("uncategorised row = %+v", rows[0])
		}
		if rows[1].Name != "Reise" || rows[1].Count != 1 || rows[1].OutgoingGrossCents != 12000 || rows[1].OutgoingNetCents != 10000 {
			t.Errorf("Reise = %+v — the Storno pair must drop out", rows[1])
		}
	})
}

func TestCategoryKeywordsAndDefaultVAT(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		ten := 10
		id, err := st.CreateCategory(ctx, store.Category{Name: "Lebensmittel", Direction: "outgoing",
			Keywords: " Billa,spar , ,BILLA,  Hofer  KG ", DefaultVATRate: &ten})
		if err != nil {
			t.Fatal(err)
		}
		c, _ := st.GetCategory(ctx, id)
		if c.Keywords != "billa, spar, hofer kg" || c.VAT() != 10 || !c.HasDefaultVAT() {
			t.Errorf("category = %q / %v", c.Keywords, c.DefaultVATRate)
		}
		if got := c.KeywordList(); len(got) != 3 || got[2] != "hofer kg" {
			t.Errorf("keyword list = %v", got)
		}
		c.DefaultVATRate = nil
		if err := st.UpdateCategory(ctx, c); err != nil {
			t.Fatal(err)
		}
		if c, _ := st.GetCategory(ctx, id); c.HasDefaultVAT() || c.VAT() != -1 {
			t.Error("the default VAT rate could not be removed")
		}
		seven := 7
		if _, err := st.CreateCategory(ctx, store.Category{Name: "X", Direction: "outgoing", DefaultVATRate: &seven}); err == nil {
			t.Error("a 7 % default rate was accepted")
		}
	})
}

func TestOutgoingVATDefault(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if s, _ := st.Settings(ctx); s.DefaultVATOutgoing != 20 || s.OutgoingVAT() != 20 {
			t.Errorf("fresh default = %d", s.DefaultVATOutgoing)
		}
		if err := st.SaveSettings(ctx, store.Settings{IssuerName: "X", DefaultVATOutgoing: 10}); err != nil {
			t.Fatal(err)
		}
		s, _ := st.Settings(ctx)
		if s.OutgoingVAT() != 10 {
			t.Errorf("outgoing VAT = %d, want 10", s.OutgoingVAT())
		}
		// A Kleinunternehmer records the receipt's VAT but deducts none
		// (ADR 0022).
		s.Kleinunternehmer = true
		if s.OutgoingVAT() != 10 || !s.KUOn("2026-01-01") {
			t.Errorf("Kleinunternehmer: outgoing VAT %d, exempt %v; want the receipt rate, exempt",
				s.OutgoingVAT(), s.KUOn("2026-01-01"))
		}
		if err := st.SaveSettings(ctx, store.Settings{IssuerName: "X", DefaultVATOutgoing: 7}); err == nil {
			t.Error("7 % accepted as default")
		}
	})
}
