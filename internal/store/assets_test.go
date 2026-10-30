package store_test

import (
	"context"
	"testing"

	"github.com/chrthal/accountant/internal/store"
)

func entry(t *testing.T, st *store.Store, date, dir, desc string, gross int64, rate int, cat *int64) int64 {
	t.Helper()
	id, err := st.CreateEntry(context.Background(), store.NewEntry{BookedOn: date, Direction: dir, Description: desc,
		AmountGrossCents: gross, VATRate: rate, CategoryID: cat})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// ADR 0037: a Bewirtung counts half for the profit and keeps its VAT; an
// asset counts by AfA, not when it was paid.
func TestDeductibleShareAndAfA(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		setupIssuer(t, st, false)
		meals, err := st.CreateCategory(ctx, store.Category{Name: "Bewirtung", Direction: "outgoing", DeductiblePct: 50})
		if err != nil {
			t.Fatal(err)
		}
		entry(t, st, "2026-02-01", "income", "Beratung", 600000, 20, nil)           // net 5.000,00
		entry(t, st, "2026-02-10", "outgoing", "Geschäftsessen", 11000, 10, &meals) // net 100,00, VAT 10,00
		laptop := entry(t, st, "2026-03-10", "outgoing", "Laptop", 180000, 20, nil) // net 1.500,00
		if _, err := st.CreateAsset(ctx, laptop, "", 3, "2026-03-10"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.CreateAsset(ctx, laptop, "", 3, "2026-03-10"); err == nil {
			t.Error("a booking became an asset twice")
		}
		income := entry(t, st, "2026-04-01", "income", "Zinsen", 100, 0, nil)
		if _, err := st.CreateAsset(ctx, income, "", 3, "2026-04-01"); err == nil {
			t.Error("income became an asset")
		}

		tot, err := st.Totals(ctx, 2026)
		if err != nil {
			t.Fatal(err)
		}
		if tot.OutgoingCostCents != 5000 || tot.NonDeductibleCents != 5000 || tot.DepreciationCents != 50000 {
			t.Errorf("cost %d, non-deductible %d, AfA %d; want 5000, 5000, 50000", tot.OutgoingCostCents, tot.NonDeductibleCents, tot.DepreciationCents)
		}
		if tot.OutgoingVATDeductibleCents() != 1000+30000 {
			t.Errorf("Vorsteuer = %d, want the full 10,00 + 300,00", tot.OutgoingVATDeductibleCents())
		}
		if want := int64(500000 + 100 - 5000 - 50000); tot.ProfitCents() != want {
			t.Errorf("profit = %d, want %d", tot.ProfitCents(), want)
		}
		if tot.OutgoingGrossCents != 11000+180000 {
			t.Error("the cash figures must still include the purchase")
		}

		rows, err := st.CategoryBreakdown(ctx, 2026)
		if err != nil {
			t.Fatal(err)
		}
		var saldo int64
		afaRow := false
		for _, r := range rows {
			saldo += r.SaldoCents()
			afaRow = afaRow || (r.ID == store.AfARow && r.OutgoingCostCents == 50000)
		}
		if !afaRow || saldo != tot.ProfitCents() {
			t.Errorf("breakdown: AfA row %v, saldo %d, profit %d", afaRow, saldo, tot.ProfitCents())
		}

		later, _ := st.Totals(ctx, 2028)
		if later.DepreciationCents != 50000 {
			t.Errorf("AfA 2028 = %d", later.DepreciationCents)
		}
		inYear, _ := st.AssetsInYear(ctx, 2027)
		if len(inYear) != 1 || inYear[0].BookValue(2027) != 50000 {
			t.Errorf("Anlagenverzeichnis 2027 = %+v", inYear)
		}

		// A reversed purchase depreciates nothing.
		if _, err := st.ReverseEntry(ctx, laptop, "2026-03-20"); err != nil {
			t.Fatal(err)
		}
		if tot, _ := st.Totals(ctx, 2026); tot.DepreciationCents != 0 {
			t.Errorf("AfA after the Storno = %d", tot.DepreciationCents)
		}
	})
}

// Under the Kleinunternehmerregelung the VAT is cost, so the asset's cost
// is the gross amount.
func TestAssetCostUnderKleinunternehmer(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		setupIssuer(t, st, true)
		id := entry(t, st, "2026-03-10", "outgoing", "Laptop", 180000, 20, nil)
		if _, err := st.CreateAsset(ctx, id, "Laptop", 3, "2026-09-01"); err != nil {
			t.Fatal(err)
		}
		a, ok, _ := st.AssetForEntry(ctx, id)
		if !ok || a.CostCents != 180000 || a.AfA(2026) != 30000 || a.LastYear() != 2029 {
			t.Errorf("asset = %+v, AfA 2026 %d, last year %d", a, a.AfA(2026), a.LastYear())
		}
	})
}
