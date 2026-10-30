package invoice_test

import (
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/invoice"
)

func input(lines ...invoice.Line) invoice.Input {
	return invoice.Input{
		Number:      "2026-0001",
		IssueDate:   "2026-08-18",
		ServiceDate: "2026-08-15",
		Issuer:      invoice.Party{Name: "Mustermann IT", Address: "Wien", UID: "ATU12345678"},
		Recipient:   invoice.Party{Name: "Muster GmbH", Address: "Graz"},
		Lines:       lines,
	}
}

func line(desc string, qtyMilli, priceCents int64, rate int) invoice.Line {
	return invoice.Line{Description: desc, QuantityMilli: qtyMilli, UnitPriceCents: priceCents, VATRate: rate}
}

// The Phase 2 acceptance case: 20 % and 10 % on one invoice, grouped per rate.
func TestMixedRateInvoice(t *testing.T) {
	doc, err := invoice.Build(input(
		line("Beratung", 1500, 10000, 20), // 1,5 × 100,00 = 150,00 net, 30,00 VAT
		line("Bewirtung", 2000, 2500, 10), // 2   ×  25,00 =  50,00 net,  5,00 VAT
	))
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if got := doc.Lines[0]; got.NetCents != 15000 || got.VATCents != 3000 || got.GrossCents != 18000 {
		t.Errorf("line 1 = (%d, %d, %d), want (15000, 3000, 18000)", got.NetCents, got.VATCents, got.GrossCents)
	}
	if got := doc.Lines[1]; got.NetCents != 5000 || got.VATCents != 500 || got.GrossCents != 5500 {
		t.Errorf("line 2 = (%d, %d, %d), want (5000, 500, 5500)", got.NetCents, got.VATCents, got.GrossCents)
	}
	if doc.NetCents != 20000 || doc.VATCents != 3500 || doc.GrossCents != 23500 {
		t.Errorf("totals = (%d, %d, %d), want (20000, 3500, 23500)", doc.NetCents, doc.VATCents, doc.GrossCents)
	}
	if doc.NetCents+doc.VATCents != doc.GrossCents {
		t.Error("net + VAT does not equal gross")
	}

	if len(doc.VATGroups) != 2 {
		t.Fatalf("got %d VAT groups, want one per rate", len(doc.VATGroups))
	}
	// § 11 wants the VAT shown per rate; the groups must reconstruct the total.
	var sum int64
	for _, g := range doc.VATGroups {
		sum += g.VATCents
	}
	if sum != doc.VATCents {
		t.Errorf("VAT groups sum to %d, invoice VAT is %d", sum, doc.VATCents)
	}
	if doc.VATGroups[0].Rate != 20 || doc.VATGroups[1].Rate != 10 {
		t.Error("VAT groups are not in the order the lines introduced them")
	}
}

func TestKleinunternehmerReplacesVATWithTheExemptionNote(t *testing.T) {
	in := input(line("Beratung", 1000, 50000, 0))
	in.Kleinunternehmer = true
	in.Issuer.UID = "" // a Kleinunternehmer typically has none

	doc, err := invoice.Build(in)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if doc.VATCents != 0 {
		t.Errorf("VAT = %d, want 0 on a Kleinunternehmer invoice", doc.VATCents)
	}
	if doc.GrossCents != doc.NetCents {
		t.Error("gross must equal net when no VAT is charged")
	}
	if !strings.Contains(doc.Note, "§ 6 Abs 1 Z 27") {
		t.Errorf("note = %q, want the exemption citation", doc.Note)
	}
	// The missing issuer UID is the point of the exemption, not a defect.
	for _, w := range doc.Warnings {
		if strings.Contains(w, "UID-Nummer des Rechnungsstellers") {
			t.Error("warned about a missing issuer UID on a Kleinunternehmer invoice")
		}
	}

	// Charging VAT while claiming the exemption is a contradiction, not a
	// rounding detail — it must be refused.
	in.Lines = []invoice.Line{line("Beratung", 1000, 50000, 20)}
	if _, err := invoice.Build(in); err == nil {
		t.Error("Kleinunternehmer invoice with a 20 % line was accepted")
	}
}

func TestBuildRejectsIncompleteInvoices(t *testing.T) {
	cases := map[string]func(in *invoice.Input){
		"no lines":         func(in *invoice.Input) { in.Lines = nil },
		"no issuer":        func(in *invoice.Input) { in.Issuer.Name = "" },
		"no recipient":     func(in *invoice.Input) { in.Recipient.Name = " " },
		"no number":        func(in *invoice.Input) { in.Number = "" },
		"bad issue date":   func(in *invoice.Input) { in.IssueDate = "18.08.2026" },
		"no service date":  func(in *invoice.Input) { in.ServiceDate = "" },
		"no description":   func(in *invoice.Input) { in.Lines[0].Description = "" },
		"zero quantity":    func(in *invoice.Input) { in.Lines[0].QuantityMilli = 0 },
		"unknown VAT rate": func(in *invoice.Input) { in.Lines[0].VATRate = 7 },
	}
	for name, mutate := range cases {
		in := input(line("Beratung", 1000, 10000, 20))
		mutate(&in)
		if _, err := invoice.Build(in); err == nil {
			t.Errorf("%s: accepted, want an error", name)
		}
	}
}

// An unexplained 0 % line beside taxed lines would print an exemption the app
// cannot name (ADR 0008).
func TestMixingZeroRateWithTaxedLinesIsRefused(t *testing.T) {
	_, err := invoice.Build(input(
		line("Beratung", 1000, 10000, 20),
		line("Durchlaufender Posten", 1000, 5000, 0),
	))
	if err == nil {
		t.Fatal("mixed 0 % and 20 % invoice was accepted")
	}
	if !strings.Contains(err.Error(), "exemption") {
		t.Errorf("error = %q, want it to explain the missing exemption reason", err)
	}
}

func TestWarnings(t *testing.T) {
	// Over €10.000 the recipient's UID is required where they are a business.
	// The app cannot know that, so this warns rather than refuses.
	in := input(line("Projekt", 1000, 1200000, 20))
	in.Recipient.UID = ""
	doc, err := invoice.Build(in)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var warned bool
	for _, w := range doc.Warnings {
		if strings.Contains(w, "10.000") {
			warned = true
		}
	}
	if !warned {
		t.Error("no warning about the recipient UID above the €10.000 threshold")
	}

	// A missing issuer UID on a normal invoice is a § 11 defect.
	in = input(line("Beratung", 1000, 10000, 20))
	in.Issuer.UID = ""
	doc, _ = invoice.Build(in)
	if len(doc.Warnings) == 0 {
		t.Error("no warning about the missing issuer UID")
	}
}

// Quantities and prices that do not divide evenly still have to add up.
func TestRoundingStaysConsistent(t *testing.T) {
	doc, err := invoice.Build(input(
		line("Drittelstunde", 333, 9999, 20),
		line("Kleinteil", 7000, 3, 20),
		line("Krumm", 1234, 567, 20),
	))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var net, vat, gross int64
	for _, l := range doc.Lines {
		if l.NetCents+l.VATCents != l.GrossCents {
			t.Errorf("line %d: net + VAT != gross", l.Position)
		}
		net, vat, gross = net+l.NetCents, vat+l.VATCents, gross+l.GrossCents
	}
	if net != doc.NetCents || vat != doc.VATCents || gross != doc.GrossCents {
		t.Error("invoice totals do not equal the sum of their lines")
	}
}

func TestDueDateIsCarriedAndChecked(t *testing.T) {
	in := input(line("Beratung", 1000, 10000, 20))
	in.DueDate = "2026-09-01"
	in.PaymentIBAN = " AT61 1904 3002 3457 3201 "
	doc, err := invoice.Build(in)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if doc.DueDate != "2026-09-01" || doc.PaymentIBAN != "AT61 1904 3002 3457 3201" {
		t.Errorf("due date / IBAN not carried: %q %q", doc.DueDate, doc.PaymentIBAN)
	}
	if _, err := invoice.RenderPDF(doc, nil); err != nil {
		t.Errorf("render with payment note: %v", err)
	}

	in.DueDate = "2026-08-01" // before the issue date
	if _, err := invoice.Build(in); err == nil {
		t.Error("a due date before the issue date was accepted")
	}
	in.DueDate = "morgen"
	if _, err := invoice.Build(in); err == nil {
		t.Error("an unparseable due date was accepted")
	}
	in.DueDate = ""
	if _, err := invoice.Build(in); err != nil {
		t.Errorf("an invoice without a due date must still build: %v", err)
	}
}
