package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/chrthal/accountant/internal/store"
)

// ADR 0036: part payments split over the VAT rates by what is still open; the
// last one books the exact rest, so a paid invoice shows its VAT to the cent.
func TestPartialPayments(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		contactID := setupIssuer(t, st, false)
		inv, err := st.CreateInvoice(ctx, store.NewInvoice{ContactID: contactID, IssueDate: "2026-05-04", ServiceDate: "2026-05-04",
			Lines: []store.InvoiceLine{
				{Description: "Beratung", QuantityMilli: 1000, UnitPriceCents: 10000, VATRate: 20}, // 120,00 gross, 20,00 VAT
				{Description: "Bewirtung", QuantityMilli: 1000, UnitPriceCents: 5000, VATRate: 10}, // 55,00 gross, 5,00 VAT
			}})
		if err != nil {
			t.Fatal(err)
		}
		pay := func(cents int64) []int64 {
			t.Helper()
			ids, err := st.BookInvoicePayment(ctx, store.InvoicePayment{InvoiceID: inv.ID, PaidOn: "2026-05-10", AmountCents: cents})
			if err != nil {
				t.Fatalf("pay %d: %v", cents, err)
			}
			return ids
		}

		first := pay(10000)
		got, _ := st.GetInvoice(ctx, inv.ID)
		if got.PaidCents != 10000 || got.OpenCents() != 7500 || got.IsPaid() {
			t.Fatalf("after 100,00: paid %d open %d", got.PaidCents, got.OpenCents())
		}
		var gross, vat int64
		for _, id := range first {
			e, _ := st.GetEntry(ctx, id)
			gross += e.AmountGrossCents
			vat += e.VATCents
			if e.Description[:11] != "Teilzahlung" {
				t.Errorf("part payment described as %q", e.Description)
			}
		}
		// 100,00 splits 120:55 into 68,57 and 31,43 (the cent left over goes
		// to the larger fraction); their VAT is 11,43 and 2,86.
		if gross != 10000 || vat != 1143+286 {
			t.Errorf("first part: gross %d vat %d, want 10000 and 1429", gross, vat)
		}

		if _, err := st.BookInvoicePayment(ctx, store.InvoicePayment{InvoiceID: inv.ID, PaidOn: "2026-05-11", AmountCents: 7501}); !errors.Is(err, store.ErrOverpaid) {
			t.Errorf("overpayment = %v, want ErrOverpaid", err)
		}

		rest := pay(0)
		got, _ = st.GetInvoice(ctx, inv.ID)
		if !got.IsPaid() || got.OpenCents() != 0 {
			t.Fatalf("after the rest: paid %d", got.PaidCents)
		}
		byRate := map[int][2]int64{}
		for _, id := range append(first, rest...) {
			e, _ := st.GetEntry(ctx, id)
			v := byRate[e.VATRate]
			byRate[e.VATRate] = [2]int64{v[0] + e.AmountGrossCents, v[1] + e.VATCents}
		}
		if byRate[20] != [2]int64{12000, 2000} || byRate[10] != [2]int64{5500, 500} {
			t.Errorf("per rate after full payment = %v, want 20%%: 120,00/20,00, 10%%: 55,00/5,00", byRate)
		}
		if _, err := st.BookInvoicePayment(ctx, store.InvoicePayment{InvoiceID: inv.ID, PaidOn: "2026-05-12"}); !errors.Is(err, store.ErrAlreadyPaid) {
			t.Errorf("paying a paid invoice = %v", err)
		}

		// A Storno of a part reopens exactly that part.
		if _, err := st.ReverseEntry(ctx, first[0], "2026-05-13"); err != nil {
			t.Fatal(err)
		}
		e, _ := st.GetEntry(ctx, first[0])
		got, _ = st.GetInvoice(ctx, inv.ID)
		if got.OpenCents() != e.AmountGrossCents {
			t.Errorf("after the Storno, open = %d, want %d", got.OpenCents(), e.AmountGrossCents)
		}
	})
}
