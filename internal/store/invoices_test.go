package store_test

import (
	"context"
	"testing"

	"github.com/chrthal/accountant/internal/store"
)

func setupIssuer(t *testing.T, st *store.Store, ku bool) int64 {
	t.Helper()
	ctx := context.Background()
	if err := st.SaveSettings(ctx, store.Settings{
		IssuerName:       "Mustermann IT",
		IssuerAddress:    "Beispielgasse 2\n1010 Wien",
		IssuerUID:        "ATU12345678",
		Kleinunternehmer: ku,
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	id, err := st.CreateContact(ctx, store.Contact{
		Kind: "customer", Name: "Muster GmbH", Address: "Musterstraße 1\n8010 Graz",
	})
	if err != nil {
		t.Fatalf("create contact: %v", err)
	}
	return id
}

func TestSettingsRoundTrip(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		want := store.Settings{
			IssuerName: "Mustermann IT", IssuerAddress: "Wien", IssuerUID: "ATU12345678",
			Kleinunternehmer: true, DateFormat: store.DefaultDateFormat,
			IssuerBIC: "ABCDATWWXXX", IssuerContact: "Tel: +43 1 234\noffice@example.at",
			InvoiceNumbering: store.NumberingDay, InvoicePrefix: "E",
		}
		if err := st.SaveSettings(ctx, want); err != nil {
			t.Fatalf("save: %v", err)
		}
		got, err := st.Settings(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if got != want {
			t.Errorf("settings = %+v, want %+v", got, want)
		}

		// Switching the exemption off needs its end date (ADR 0022).
		want.Kleinunternehmer = false
		if err := st.SaveSettings(ctx, want); err == nil {
			t.Error("the Kleinunternehmerregelung was switched off without an end date")
		}
		// Saving twice must replace, not accumulate or fail.
		want.KleinunternehmerFrom, want.KleinunternehmerUntil = "2025-01-01", "2026-06-30"
		if err := st.SaveSettings(ctx, want); err != nil {
			t.Fatalf("second save: %v", err)
		}
		got, _ = st.Settings(ctx)
		if got.Kleinunternehmer {
			t.Error("Kleinunternehmer flag did not turn off")
		}
		// The period must survive the round trip: without its end date the
		// exempt months would count as deductible (ADR 0022).
		if got.KleinunternehmerFrom != "2025-01-01" || got.KleinunternehmerUntil != "2026-06-30" ||
			!got.KUOn("2026-06-30") || got.KUOn("2026-07-01") || got.KUOn("2024-12-31") {
			t.Errorf("period after save = %q–%q", got.KleinunternehmerFrom, got.KleinunternehmerUntil)
		}

		if err := st.SaveSettings(ctx, store.Settings{}); err == nil {
			t.Error("settings without an issuer name were accepted")
		}
	})
}

func TestCreateInvoiceNumbersSequentiallyPerYear(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		contactID := setupIssuer(t, st, false)

		mk := func(date string) store.Invoice {
			t.Helper()
			inv, err := st.CreateInvoice(ctx, store.NewInvoice{
				ContactID: contactID, IssueDate: date, ServiceDate: date,
				Lines: []store.InvoiceLine{{
					Description: "Beratung", QuantityMilli: 1000,
					UnitPriceCents: 10000, VATRate: 20,
				}},
			})
			if err != nil {
				t.Fatalf("create invoice %s: %v", date, err)
			}
			return inv
		}

		// A fresh year starts at 0001 — the sequence is per year (ADR 0008).
		first := mk("2040-01-10")
		second := mk("2040-06-01")
		nextYear := mk("2041-01-02")

		if first.Number != "2040-0001" {
			t.Errorf("first number = %q, want 2040-0001", first.Number)
		}
		if second.Number != "2040-0002" {
			t.Errorf("second number = %q, want 2040-0002", second.Number)
		}
		if nextYear.Number != "2041-0001" {
			t.Errorf("new year number = %q, want 2041-0001", nextYear.Number)
		}
	})
}

func TestInvoiceStoresComputedAmountsAndSnapshots(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		contactID := setupIssuer(t, st, false)

		inv, err := st.CreateInvoice(ctx, store.NewInvoice{
			ContactID: contactID, IssueDate: "2042-03-01", ServiceDate: "2042-02-28",
			Lines: []store.InvoiceLine{
				{Description: "Beratung", QuantityMilli: 1500, UnitPriceCents: 10000, VATRate: 20},
				{Description: "Verpflegung", QuantityMilli: 2000, UnitPriceCents: 2500, VATRate: 10},
			},
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		if inv.NetCents != 20000 || inv.VATCents != 3500 || inv.GrossCents != 23500 {
			t.Errorf("totals = (%d, %d, %d), want (20000, 3500, 23500)",
				inv.NetCents, inv.VATCents, inv.GrossCents)
		}
		if len(inv.Lines) != 2 {
			t.Fatalf("got %d lines, want 2", len(inv.Lines))
		}
		if inv.IssuerName != "Mustermann IT" || inv.RecipientName != "Muster GmbH" {
			t.Error("parties were not snapshotted onto the invoice")
		}

		// Changing the issuer afterwards must not touch an issued invoice.
		if err := st.SaveSettings(ctx, store.Settings{IssuerName: "Umbenannt GmbH"}); err != nil {
			t.Fatalf("save settings: %v", err)
		}
		again, err := st.GetInvoice(ctx, inv.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if again.IssuerName != "Mustermann IT" {
			t.Errorf("issuer on the issued invoice changed to %q", again.IssuerName)
		}

		// The renderable document must reproduce the stored numbers exactly and
		// group VAT per rate.
		doc := again.Document()
		if doc.GrossCents != 23500 || len(doc.VATGroups) != 2 {
			t.Errorf("document = %d gross, %d VAT groups; want 23500 and 2",
				doc.GrossCents, len(doc.VATGroups))
		}
	})
}

func TestKleinunternehmerInvoiceCarriesNoVAT(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		contactID := setupIssuer(t, st, true)

		inv, err := st.CreateInvoice(ctx, store.NewInvoice{
			ContactID: contactID, IssueDate: "2043-05-05", ServiceDate: "2043-05-05",
			Lines: []store.InvoiceLine{{
				Description: "Beratung", QuantityMilli: 1000, UnitPriceCents: 120000, VATRate: 0,
			}},
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if inv.VATCents != 0 || inv.GrossCents != inv.NetCents {
			t.Error("a Kleinunternehmer invoice charged VAT")
		}
		if !inv.Kleinunternehmer || inv.Note == "" {
			t.Error("exemption note missing from the stored invoice")
		}

		// Charging VAT while the exemption applies is refused, not silently fixed.
		if _, err := st.CreateInvoice(ctx, store.NewInvoice{
			ContactID: contactID, IssueDate: "2043-05-06", ServiceDate: "2043-05-06",
			Lines: []store.InvoiceLine{{
				Description: "Beratung", QuantityMilli: 1000, UnitPriceCents: 1000, VATRate: 20,
			}},
		}); err == nil {
			t.Error("Kleinunternehmer invoice with VAT was accepted")
		}
	})
}

func TestInvoicesAreImmutableInTheDatabase(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		contactID := setupIssuer(t, st, false)
		inv, err := st.CreateInvoice(ctx, store.NewInvoice{
			ContactID: contactID, IssueDate: "2044-01-01", ServiceDate: "2044-01-01",
			Lines: []store.InvoiceLine{{
				Description: "Beratung", QuantityMilli: 1000, UnitPriceCents: 10000, VATRate: 20,
			}},
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		if _, err := st.DB.ExecContext(ctx,
			st.Rebind("UPDATE invoices SET gross_cents = 1 WHERE id = ?"), inv.ID); err == nil {
			t.Error("UPDATE on invoices succeeded — an issued invoice must be immutable")
		}
		if _, err := st.DB.ExecContext(ctx,
			st.Rebind("DELETE FROM invoice_lines WHERE invoice_id = ?"), inv.ID); err == nil {
			t.Error("DELETE on invoice_lines succeeded — an issued invoice must be immutable")
		}
	})
}

func TestCreateInvoiceRefusesIncompleteSetup(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()

		// No issuer configured yet.
		if _, err := st.CreateInvoice(ctx, store.NewInvoice{
			ContactID: 1, IssueDate: "2045-01-01", ServiceDate: "2045-01-01",
			Lines: []store.InvoiceLine{{Description: "x", QuantityMilli: 1000, UnitPriceCents: 100, VATRate: 20}},
		}); err == nil {
			t.Error("invoice created without issuer settings")
		}

		contactID := setupIssuer(t, st, false)
		bad := map[string]store.NewInvoice{
			"no lines": {ContactID: contactID, IssueDate: "2045-01-01", ServiceDate: "2045-01-01"},
			"unknown contact": {ContactID: 999999, IssueDate: "2045-01-01", ServiceDate: "2045-01-01",
				Lines: []store.InvoiceLine{{Description: "x", QuantityMilli: 1000, UnitPriceCents: 100, VATRate: 20}}},
			"bad date": {ContactID: contactID, IssueDate: "01.01.2045", ServiceDate: "2045-01-01",
				Lines: []store.InvoiceLine{{Description: "x", QuantityMilli: 1000, UnitPriceCents: 100, VATRate: 20}}},
		}
		for name, in := range bad {
			if _, err := st.CreateInvoice(ctx, in); err == nil {
				t.Errorf("%s: accepted, want an error", name)
			}
		}
	})
}

// The Phase 8 acceptance case at store level: a mixed-rate invoice is paid in
// one step, the ledger carries the invoice's own VAT to the cent, and a Storno
// of one payment entry reopens it.
func TestInvoicePaymentBooksOneEntryPerRate(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		contact := setupIssuer(t, st, false)
		cat, _ := st.CreateCategory(ctx, store.Category{Name: "Erlöse", Direction: "income",
			DefaultKonto: "4000", DefaultGegenkonto: "2800"})

		inv, err := st.CreateInvoice(ctx, store.NewInvoice{
			ContactID: contact, IssueDate: "2026-08-18", ServiceDate: "2026-08-15", DueDate: "2026-09-01",
			Lines: []store.InvoiceLine{
				{Description: "Beratung", QuantityMilli: 1500, Unit: "Std.", UnitPriceCents: 12000, VATRate: 20},
				{Description: "Bücher", QuantityMilli: 3000, Unit: "Stk.", UnitPriceCents: 2490, VATRate: 10},
				{Description: "Kulanz", QuantityMilli: 1000, UnitPriceCents: 0, VATRate: 20},
			},
		})
		if err != nil {
			t.Fatalf("create invoice: %v", err)
		}
		if inv.DueDate != "2026-09-01" || inv.IsPaid() || inv.OpenCents() != inv.GrossCents {
			t.Fatalf("fresh invoice = due %q paid %v open %d", inv.DueDate, inv.IsPaid(), inv.OpenCents())
		}
		if !inv.IsOverdue() {
			t.Error("an unpaid invoice due 2026-09-01 is not overdue")
		}

		ids, err := st.BookInvoicePayment(ctx, store.InvoicePayment{
			InvoiceID: inv.ID, PaidOn: "2026-08-30", CategoryID: &cat})
		if err != nil {
			t.Fatalf("book payment: %v", err)
		}
		if len(ids) != 2 {
			t.Fatalf("payment entries = %d, want 2 (one per VAT rate)", len(ids))
		}

		var gross, vat int64
		for _, id := range ids {
			e, _ := st.GetEntry(ctx, id)
			gross += e.AmountGrossCents
			vat += e.VATCents
			if e.InvoiceID == nil || *e.InvoiceID != inv.ID || e.InvoiceNumber != inv.Number {
				t.Errorf("entry #%d is not linked to the invoice", id)
			}
			if e.Direction != "income" || e.BookedOn != "2026-08-30" || e.Konto != "4000" {
				t.Errorf("entry #%d = %+v", id, e)
			}
		}
		if gross != inv.GrossCents || vat != inv.VATCents {
			t.Errorf("booked %d gross / %d VAT, invoice says %d / %d", gross, vat, inv.GrossCents, inv.VATCents)
		}

		paid, _ := st.GetInvoice(ctx, inv.ID)
		if !paid.IsPaid() || paid.IsOverdue() || paid.LastPaidOn != "2026-08-30" {
			t.Errorf("after payment: paid %v overdue %v last %q", paid.IsPaid(), paid.IsOverdue(), paid.LastPaidOn)
		}
		if _, err := st.BookInvoicePayment(ctx, store.InvoicePayment{InvoiceID: inv.ID, PaidOn: "2026-08-31"}); err != store.ErrAlreadyPaid {
			t.Errorf("second payment = %v, want ErrAlreadyPaid", err)
		}

		if _, err := st.ReverseEntry(ctx, ids[1], "2026-09-02"); err != nil {
			t.Fatalf("reverse: %v", err)
		}
		reopened, _ := st.GetInvoice(ctx, inv.ID)
		if reopened.IsPaid() {
			t.Error("the invoice stays paid after a payment entry was reversed")
		}
		all, _ := st.EntriesForInvoice(ctx, inv.ID)
		if len(all) != 3 {
			t.Errorf("entries for invoice = %d, want 2 payments + 1 Storno", len(all))
		}
	})
}

func TestPaymentTermsAndIBANAreSettings(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		in := store.Settings{IssuerName: "X", IssuerIBAN: "AT61 1904 3002 3457 3201", PaymentTermsDays: 30}
		if err := st.SaveSettings(ctx, in); err != nil {
			t.Fatal(err)
		}
		got, _ := st.Settings(ctx)
		if got.IssuerIBAN != in.IssuerIBAN || got.PaymentTermsDays != 30 {
			t.Errorf("settings = %+v", got)
		}
		if err := st.SaveSettings(ctx, store.Settings{IssuerName: "X", PaymentTermsDays: -1}); err == nil {
			t.Error("negative payment terms accepted")
		}
	})
}

// ADR 0025: the day scheme counts per day, so the number shows nothing about
// the year's volume. Switching schemes mid-year keeps both sequences apart.
func TestInvoiceNumbersPerDay(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		contactID := setupIssuer(t, st, false)
		mk := func(date string) string {
			t.Helper()
			inv, err := st.CreateInvoice(ctx, store.NewInvoice{
				ContactID: contactID, IssueDate: date, ServiceDate: date,
				Lines: []store.InvoiceLine{{Description: "Website", QuantityMilli: 1000, UnitPriceCents: 170000, VATRate: 20}},
			})
			if err != nil {
				t.Fatalf("create invoice %s: %v", date, err)
			}
			return inv.Number
		}
		scheme := func(numbering, prefix string) {
			t.Helper()
			s, err := st.Settings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			s.InvoiceNumbering, s.InvoicePrefix = numbering, prefix
			if err := st.SaveSettings(ctx, s); err != nil {
				t.Fatal(err)
			}
		}

		yearly := mk("2026-01-05")
		scheme(store.NumberingDay, "E")
		got := []string{yearly, mk("2026-01-12"), mk("2026-01-12"), mk("2026-12-31"), mk("2027-01-12")}
		scheme(store.NumberingYear, "")
		got = append(got, mk("2026-02-01"))

		want := []string{"2026-0001", "E260112001", "E260112002", "E261231001", "E270112001", "2026-0002"}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("invoice %d = %q, want %q", i+1, got[i], want[i])
			}
		}

		s, _ := st.Settings(ctx)
		s.InvoicePrefix = "RE 1"
		if err := st.SaveSettings(ctx, s); err == nil {
			t.Error("a prefix with a space was accepted")
		}
	})
}
