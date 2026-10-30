package store_test

import (
	"context"
	"testing"

	"github.com/chrthal/accountant/internal/store"
)

// Phase 11 acceptance: a seeded quarter against a hand calculation.
//
//	Q1 income:  1.200,00 @ 20 % → net 1.000,00, USt 200,00
//	              110,00 @ 10 % → net   100,00, USt  10,00
//	              500,00 @  0 % → exempt 500,00
//	Q1 costs:     240,00 @ 20 % → Vorsteuer 40,00
//	               50,00 @  0 % → no Vorsteuer
//	Storno pair:  999,99 @ 20 % → must not count
//	Zahllast Q1:  200 + 10 − 40 = 170,00
func TestVATPeriodsMatchAHandCalculation(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		mk := func(date, dir string, cents int64, rate int) int64 {
			n := sampleEntry()
			n.BookedOn, n.Direction, n.AmountGrossCents, n.VATRate = date, dir, cents, rate
			id, err := st.CreateEntry(ctx, n)
			if err != nil {
				t.Fatal(err)
			}
			return id
		}
		mk("2026-01-10", "income", 120000, 20)
		mk("2026-02-10", "income", 11000, 10)
		mk("2026-03-10", "income", 50000, 0)
		mk("2026-03-20", "outgoing", 24000, 20)
		mk("2026-03-21", "outgoing", 5000, 0)
		wrong := mk("2026-02-01", "income", 99999, 20)
		st.ReverseEntry(ctx, wrong, "2026-04-02")
		mk("2026-04-15", "outgoing", 12000, 20)

		qs, err := st.VATPeriods(ctx, 2026, true)
		if err != nil || len(qs) != 4 {
			t.Fatalf("quarters = %d, %v", len(qs), err)
		}
		q1 := qs[0]
		if q1.OutputVATCents != 21000 || q1.OutputNetCents != 110000 || q1.ExemptNetCents != 50000 {
			t.Errorf("Q1 output = net %d VAT %d exempt %d", q1.OutputNetCents, q1.OutputVATCents, q1.ExemptNetCents)
		}
		if q1.InputVATCents != 4000 || q1.PayableCents() != 17000 {
			t.Errorf("Q1 Vorsteuer %d, Zahllast %d, want 4000 / 17000", q1.InputVATCents, q1.PayableCents())
		}
		if len(q1.Output) != 2 || q1.Output[0].Rate != 20 || q1.Output[0].VATCents != 20000 || q1.Output[1].VATCents != 1000 {
			t.Errorf("Q1 by rate = %+v", q1.Output)
		}
		if q1.From != "2026-01-01" || q1.To != "2026-03-31" {
			t.Errorf("Q1 range = %s–%s", q1.From, q1.To)
		}
		if qs[1].PayableCents() != -2000 {
			t.Errorf("Q2 = %d, want a Gutschrift of 20,00", qs[1].PayableCents())
		}

		ms, _ := st.VATPeriods(ctx, 2026, false)
		if len(ms) != 12 || ms[1].OutputVATCents != 1000 || ms[1].To != "2026-02-28" {
			t.Errorf("February = %+v", ms[1])
		}
	})
}
