package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/chrthal/accountant/internal/store"
)

// ADR 0045: a changed business share counts in every figure, the booking row
// stays as written, and the history keeps every version.
func TestChangeBusinessShare(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		setupIssuer(t, st, false)
		phone := entry(t, st, "2026-02-03", "outgoing", "Handy", 6000, 20, nil) // net 50,00, VAT 10,00
		if err := st.ChangeBusinessShare(ctx, phone, 100, ""); !errors.Is(err, store.ErrSameShare) {
			t.Errorf("unchanged share = %v", err)
		}
		if err := st.ChangeBusinessShare(ctx, phone, 0, ""); err == nil {
			t.Error("0 % accepted")
		}
		if err := st.ChangeBusinessShare(ctx, phone, 60, "Einzelverbindungsnachweis"); err != nil {
			t.Fatal(err)
		}

		e, _ := st.GetEntry(ctx, phone)
		if e.BusinessPct != 60 || e.BusinessNote != "Einzelverbindungsnachweis" || e.AmountGrossCents != 6000 {
			t.Errorf("entry = %d %% %q %d", e.BusinessPct, e.BusinessNote, e.AmountGrossCents)
		}
		tot, _ := st.Totals(ctx, 2026)
		if tot.OutgoingVATDeductibleCents() != 600 || tot.OutgoingCostCents != 3000 || tot.PrivateShareCents != 2400 {
			t.Errorf("totals: Vorsteuer %d, cost %d, private %d", tot.OutgoingVATDeductibleCents(), tot.OutgoingCostCents, tot.PrivateShareCents)
		}
		if p, _ := st.VATPeriods(ctx, 2026, false); p[1].InputVATCents != 600 || p[1].InputVATPrivateCents != 400 {
			t.Errorf("February: %+v", p[1])
		}
		h, err := st.ShareHistory(ctx, phone)
		if err != nil || len(h) != 2 || !h[0].Original || h[0].BusinessPct != 100 || h[1].BusinessPct != 60 {
			t.Errorf("history = %+v, %v", h, err)
		}
		if _, err := st.DB.ExecContext(ctx, `UPDATE entry_shares SET business_pct = 1`); err == nil {
			t.Error("a share change was overwritten")
		}

		// Back to 100 % drops the note; the Storno takes the share in force.
		if err := st.ChangeBusinessShare(ctx, phone, 80, ""); err != nil {
			t.Fatal(err)
		}
		storno, _ := st.ReverseEntry(ctx, phone, "2026-02-10")
		if s, _ := st.GetEntry(ctx, storno); s.BusinessPct != 80 {
			t.Errorf("Storno share = %d", s.BusinessPct)
		}
		if err := st.ChangeBusinessShare(ctx, phone, 50, ""); err == nil {
			t.Error("a reversed booking changed")
		}

		income := entry(t, st, "2026-02-04", "income", "Honorar", 12000, 20, nil)
		if err := st.ChangeBusinessShare(ctx, income, 50, ""); err == nil {
			t.Error("income got a private share")
		}
		laptop := entry(t, st, "2026-02-05", "outgoing", "Laptop", 180000, 20, nil)
		if _, err := st.CreateAsset(ctx, laptop, "", 3, "2026-02-05"); err != nil {
			t.Fatal(err)
		}
		if err := st.ChangeBusinessShare(ctx, laptop, 70, ""); err == nil {
			t.Error("an asset's share changed under its fixed cost")
		}
	})
}
