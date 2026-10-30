// Package invoice builds and totals an invoice document.
//
// Everything here is plain Go over integer cents: the arithmetic and the § 11
// UStG completeness rules live where they can be tested, and the PDF writer
// (pdf.go) only draws the result (ADR 0008).
//
// Scope is domestic Austrian invoices — ordinary VAT rates, or the
// Kleinunternehmer exemption. Reverse charge, innergemeinschaftliche
// Lieferungen and cross-border Kleinunternehmer rules change the mandatory
// fields and are deliberately out of scope; see ADR 0008.
package invoice

import (
	"fmt"
	"strings"
	"time"

	"github.com/chrthal/accountant/internal/money"
)

// KleinunternehmerNote is the wording put on an invoice in place of a VAT
// block. § 6 Abs 1 Z 27 UStG requires the exemption to be cited; the exact
// phrasing is conventional, not prescribed.
const KleinunternehmerNote = "Umsatzsteuerbefreit gemäß § 6 Abs 1 Z 27 UStG (Kleinunternehmerregelung)"

// RecipientUIDThresholdCents is the invoice total above which the recipient's
// own UID must appear, where the recipient is a business (§ 11 UStG, €10.000).
// Whether the recipient is a business is not something this app knows, so
// crossing it produces a warning for the user rather than a refusal.
const RecipientUIDThresholdCents int64 = 1_000_000

// Party is one side of the invoice, snapshotted at issue time (ADR 0008).
type Party struct {
	Name    string
	Address string
	UID     string
}

// Line is one invoice position. The cent amounts are computed by Build.
type Line struct {
	Position       int
	Description    string
	QuantityMilli  int64 // thousandths, so 1,5 h is 1500
	Unit           string
	UnitPriceCents int64 // net, per unit
	VATRate        int

	NetCents   int64
	VATCents   int64
	GrossCents int64
}

// VATGroup is the per-rate summary § 11 requires on a mixed-rate invoice.
type VATGroup struct {
	Rate     int
	NetCents int64
	VATCents int64
}

// Document is a complete, totalled invoice.
type Document struct {
	Number      string
	IssueDate   string // 'YYYY-MM-DD'
	ServiceDate string // Liefer-/Leistungsdatum
	DueDate     string // 'YYYY-MM-DD', or empty for no stated due date
	PaymentIBAN string // the issuer's account for the payment note, optional
	PaymentBIC  string // optional within the SEPA area
	Contact     string // free text for the footer: phone, hours, email

	Issuer    Party
	Recipient Party

	Kleinunternehmer bool
	Note             string
	// Draft marks a preview: the PDF says so on every page and leaves out
	// the payment QR code, so a draft cannot be paid by mistake.
	Draft bool

	Lines     []Line
	VATGroups []VATGroup

	NetCents   int64
	VATCents   int64
	GrossCents int64

	// Warnings are things a person should look at before sending — not reasons
	// to refuse the document.
	Warnings []string
}

// Input is what Build needs; everything else it derives.
type Input struct {
	Number           string
	IssueDate        string
	ServiceDate      string
	DueDate          string
	PaymentIBAN      string
	PaymentBIC       string
	Contact          string
	Issuer           Party
	Recipient        Party
	Kleinunternehmer bool
	Note             string
	Lines            []Line
}

// Build validates the input, computes every amount, and returns the document.
//
// Line net is unit price × quantity; line VAT is computed from that net, and
// the invoice totals are the sums of the lines. Totalling per line rather than
// re-deriving VAT from the invoice net is what keeps the printed lines and the
// printed total consistent to the cent.
func Build(in Input) (Document, error) {
	if strings.TrimSpace(in.Issuer.Name) == "" {
		return Document{}, fmt.Errorf("issuer name is required (§ 11 Abs 1 Z 1 UStG)")
	}
	if strings.TrimSpace(in.Recipient.Name) == "" {
		return Document{}, fmt.Errorf("recipient name is required (§ 11 Abs 1 Z 2 UStG)")
	}
	if strings.TrimSpace(in.Number) == "" {
		return Document{}, fmt.Errorf("invoice number is required (§ 11 Abs 1 Z 6 UStG)")
	}
	if _, err := time.Parse("2006-01-02", in.IssueDate); err != nil {
		return Document{}, fmt.Errorf("issue date must be YYYY-MM-DD")
	}
	if _, err := time.Parse("2006-01-02", in.ServiceDate); err != nil {
		return Document{}, fmt.Errorf("service date must be YYYY-MM-DD (§ 11 Abs 1 Z 4 UStG)")
	}
	if in.DueDate != "" {
		due, err := time.Parse("2006-01-02", in.DueDate)
		if err != nil {
			return Document{}, fmt.Errorf("due date must be YYYY-MM-DD")
		}
		if issued, _ := time.Parse("2006-01-02", in.IssueDate); due.Before(issued) {
			return Document{}, fmt.Errorf("the due date cannot be before the issue date")
		}
	}
	if len(in.Lines) == 0 {
		return Document{}, fmt.Errorf("an invoice needs at least one line")
	}

	doc := Document{
		Number:           in.Number,
		IssueDate:        in.IssueDate,
		ServiceDate:      in.ServiceDate,
		DueDate:          in.DueDate,
		PaymentIBAN:      strings.TrimSpace(in.PaymentIBAN),
		PaymentBIC:       strings.TrimSpace(in.PaymentBIC),
		Contact:          strings.TrimSpace(in.Contact),
		Issuer:           in.Issuer,
		Recipient:        in.Recipient,
		Kleinunternehmer: in.Kleinunternehmer,
		Note:             in.Note,
	}

	var (
		byRate    = map[int]*VATGroup{}
		rateOrder []int
		anyTaxed  bool
	)

	for i, l := range in.Lines {
		if strings.TrimSpace(l.Description) == "" {
			return Document{}, fmt.Errorf("line %d: description is required (§ 11 Abs 1 Z 3 UStG)", i+1)
		}
		if l.QuantityMilli <= 0 {
			return Document{}, fmt.Errorf("line %d: quantity must be greater than zero", i+1)
		}
		if l.UnitPriceCents < 0 {
			return Document{}, fmt.Errorf("line %d: unit price cannot be negative", i+1)
		}
		if !money.IsValidVATRate(l.VATRate) {
			return Document{}, fmt.Errorf("line %d: VAT rate must be one of 0, 10, 13, 20", i+1)
		}
		if in.Kleinunternehmer && l.VATRate != 0 {
			return Document{}, fmt.Errorf(
				"line %d: a Kleinunternehmer invoice cannot carry VAT — every line must be 0 %%", i+1)
		}
		if l.VATRate != 0 {
			anyTaxed = true
		}

		l.Position = i + 1
		l.NetCents = money.LineNet(l.UnitPriceCents, l.QuantityMilli)
		l.VATCents = money.VATFromNet(l.NetCents, l.VATRate)
		l.GrossCents = l.NetCents + l.VATCents

		if _, ok := byRate[l.VATRate]; !ok {
			byRate[l.VATRate] = &VATGroup{Rate: l.VATRate}
			rateOrder = append(rateOrder, l.VATRate)
		}
		g := byRate[l.VATRate]
		g.NetCents += l.NetCents
		g.VATCents += l.VATCents

		doc.Lines = append(doc.Lines, l)
		doc.NetCents += l.NetCents
		doc.VATCents += l.VATCents
		doc.GrossCents += l.GrossCents
	}

	// A 0 % line next to taxed lines means an exemption this app cannot name,
	// and § 11 requires the reason to be stated. Refusing beats printing an
	// invoice with an unexplained exempt position (ADR 0008).
	if !in.Kleinunternehmer && anyTaxed {
		if _, hasZero := byRate[0]; hasZero {
			return Document{}, fmt.Errorf(
				"mixing 0 %% lines with taxed lines needs a stated exemption reason — not supported")
		}
	}

	for _, rate := range rateOrder {
		doc.VATGroups = append(doc.VATGroups, *byRate[rate])
	}

	if in.Kleinunternehmer {
		doc.Note = KleinunternehmerNote
		if in.Note != "" {
			doc.Note = KleinunternehmerNote + "\n" + in.Note
		}
	}

	// § 11 wants the issuer's UID on a normal invoice. A Kleinunternehmer often
	// has none, which is exactly why the exemption note replaces it.
	if !in.Kleinunternehmer && strings.TrimSpace(in.Issuer.UID) == "" {
		doc.Warnings = append(doc.Warnings,
			"Die UID-Nummer des Rechnungsstellers fehlt (§ 11 Abs 1 Z 1 UStG).")
	}
	if doc.GrossCents > RecipientUIDThresholdCents && strings.TrimSpace(in.Recipient.UID) == "" {
		doc.Warnings = append(doc.Warnings,
			"Ab 10.000 € muss die UID-Nummer des Empfängers angeführt werden, wenn dieser Unternehmer ist.")
	}

	return doc, nil
}
