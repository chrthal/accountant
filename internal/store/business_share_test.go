package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/export"
	"github.com/chrthal/accountant/internal/store"
)

func mixed(t *testing.T, st *store.Store, date, desc string, gross int64, rate, pct int, cat *int64) int64 {
	t.Helper()
	id, err := st.CreateEntry(context.Background(), store.NewEntry{BookedOn: date, Direction: "outgoing", Description: desc,
		AmountGrossCents: gross, VATRate: rate, CategoryID: cat, BusinessPct: pct, BusinessNote: "Einzelverbindungsnachweis"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// ADR 0041: the business share lowers the cost and the Vorsteuer; under
// 10 % business use none of the VAT is Vorsteuer.
func TestBusinessShare(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		setupIssuer(t, st, false)
		meals, err := st.CreateCategory(ctx, store.Category{Name: "Bewirtung", Direction: "outgoing", DeductiblePct: 50})
		if err != nil {
			t.Fatal(err)
		}
		phone := mixed(t, st, "2026-02-03", "Handyrechnung", 6000, 20, 60, nil) // net 50,00, VAT 10,00
		mixed(t, st, "2026-02-04", "Streaming", 1200, 20, 5, nil)               // net 10,00, VAT 2,00
		mixed(t, st, "2026-02-05", "Essen mit Kunde", 11000, 10, 80, &meals)    // net 100,00, VAT 10,00
		entry(t, st, "2026-02-06", "outgoing", "Büromaterial", 1200, 20, nil)   // net 10,00, VAT 2,00

		got, err := st.GetEntry(ctx, phone)
		if err != nil {
			t.Fatal(err)
		}
		if got.BusinessPct != 60 || got.BusinessNote != "Einzelverbindungsnachweis" || got.BusinessVATCents() != 600 {
			t.Errorf("entry = %d %% %q, Vorsteuer %d", got.BusinessPct, got.BusinessNote, got.BusinessVATCents())
		}
		if plain, _ := st.ListEntries(ctx, 1); plain[0].BusinessPct != 100 || plain[0].IsMixed() {
			t.Errorf("an ordinary booking has a share of %d %%", plain[0].BusinessPct)
		}

		tot, err := st.Totals(ctx, 2026)
		if err != nil {
			t.Fatal(err)
		}
		if tot.OutgoingVATPrivateCents != 400+200+200 || tot.OutgoingVATDeductibleCents() != 600+0+800+200 {
			t.Errorf("private VAT %d, Vorsteuer %d", tot.OutgoingVATPrivateCents, tot.OutgoingVATDeductibleCents())
		}
		// Phone 50,00 × 60 %, streaming 10,00 × 5 %, meal 100,00 × 80 % × 50 %.
		if tot.OutgoingCostCents != 3000+50+4000+1000 || tot.NonDeductibleCents != 4000 {
			t.Errorf("cost %d, non-deductible %d", tot.OutgoingCostCents, tot.NonDeductibleCents)
		}
		if tot.PrivateShareCents != 2400+1140+2200 {
			t.Errorf("private share = %d", tot.PrivateShareCents)
		}
		if tot.OutgoingGrossCents != 6000+1200+11000+1200 {
			t.Error("the cash figures must keep the full amounts")
		}

		periods, err := st.VATPeriods(ctx, 2026, false)
		if err != nil {
			t.Fatal(err)
		}
		feb := periods[1]
		if feb.InputVATCents != tot.OutgoingVATDeductibleCents() || feb.InputVATPrivateCents != tot.OutgoingVATPrivateCents ||
			feb.InputNetCents != 3000+50+8000+1000 {
			t.Errorf("February: Vorsteuer %d, private %d, net %d", feb.InputVATCents, feb.InputVATPrivateCents, feb.InputNetCents)
		}

		bookings, err := st.BookingsForExport(ctx, 2026)
		if err != nil {
			t.Fatal(err)
		}
		csv := string(export.CSV(bookings))
		for _, want := range []string{"betrieblich 60 %, privat 40 % (Einzelverbindungsnachweis)", "nein (betrieblich unter 10 %)",
			"Ausgaben, Privatanteil (USt nicht abziehbar);20%;29,50;6,00;35,50"} {
			if !strings.Contains(csv, want) {
				t.Errorf("CSV lacks %q", want)
			}
		}

		// The Storno carries the share, so the pair cancels in every figure.
		storno, err := st.ReverseEntry(ctx, phone, "2026-02-20")
		if err != nil {
			t.Fatal(err)
		}
		if s, _ := st.GetEntry(ctx, storno); s.BusinessPct != 60 {
			t.Errorf("Storno share = %d", s.BusinessPct)
		}
		if after, _ := st.Totals(ctx, 2026); after.OutgoingVATPrivateCents != 400 || after.OutgoingCostCents != 5050 {
			t.Errorf("after the Storno: private VAT %d, cost %d", after.OutgoingVATPrivateCents, after.OutgoingCostCents)
		}
	})
}

func TestBusinessShareRules(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		setupIssuer(t, st, false)
		for _, n := range []store.NewEntry{
			{Direction: "income", BusinessPct: 50},
			{Direction: "outgoing", BusinessPct: 101},
			{Direction: "outgoing", BusinessPct: -1},
		} {
			n.BookedOn, n.Description, n.AmountGrossCents, n.VATRate = "2026-03-01", "x", 1000, 20
			if _, err := st.CreateEntry(ctx, n); err == nil {
				t.Errorf("accepted %s at %d %%", n.Direction, n.BusinessPct)
			}
		}

		// Only the business share of an asset is written off.
		laptop := mixed(t, st, "2026-03-10", "Laptop", 180000, 20, 70, nil) // net 1.500,00
		if _, err := st.CreateAsset(ctx, laptop, "", 3, "2026-03-10"); err != nil {
			t.Fatal(err)
		}
		if a, _, _ := st.AssetForEntry(ctx, laptop); a.CostCents != 105000 {
			t.Errorf("asset cost = %d, want 70 %% of the net", a.CostCents)
		}
	})
}

// In the Kleinunternehmer time no VAT is Vorsteuer anyway: it is counted
// as blocked once, not again as private, and the cost is gross × share.
func TestBusinessShareUnderKleinunternehmer(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		setupIssuer(t, st, true)
		mixed(t, st, "2026-02-03", "Internet", 6000, 20, 50, nil)
		tot, err := st.Totals(ctx, 2026)
		if err != nil {
			t.Fatal(err)
		}
		if tot.OutgoingVATBlockedCents != 1000 || tot.OutgoingVATPrivateCents != 0 || tot.OutgoingVATDeductibleCents() != 0 {
			t.Errorf("blocked %d, private %d", tot.OutgoingVATBlockedCents, tot.OutgoingVATPrivateCents)
		}
		if tot.OutgoingCostCents != 3000 {
			t.Errorf("cost = %d, want half the gross", tot.OutgoingCostCents)
		}
		periods, _ := st.VATPeriods(ctx, 2026, false)
		if periods[1].InputVATBlockedCents != 1000 || periods[1].InputVATCents != 0 || periods[1].InputVATPrivateCents != 0 {
			t.Errorf("February = %+v", periods[1])
		}
	})
}
