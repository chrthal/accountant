// Package export writes the ledger out for other people's software: a plain CSV
// anyone can read, and a DATEV EXTF Buchungsstapel (ADR 0003, ADR 0010).
package export

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"

	"github.com/chrthal/accountant/internal/money"
)

// Booking is one row to export. It mirrors a ledger entry, kept separate so the
// exporters do not depend on the store's shape.
type Booking struct {
	ID               int64
	BookedOn         string // 'YYYY-MM-DD'
	Direction        string // income | outgoing
	Description      string
	AmountGrossCents int64
	VATRate          int
	VATCents         int64
	NetCents         int64
	Konto            string
	Gegenkonto       string
	ContactName      string
	IsReversal       bool
	ReversesEntryID  *int64
	IsReversed       bool
	// VATNotDeductible marks an expense booked while the
	// Kleinunternehmerregelung applied: its VAT is cost, not Vorsteuer.
	VATNotDeductible bool
	// BusinessPct is the business share of an expense used also privately
	// (ADR 0041); 0 or 100 for all others.
	BusinessPct int
	// Receipts names the booking's Belege: file names in the CSV download,
	// paths inside the ZIP in the archive (ADR 0029).
	Receipts []string
	// Note says how the booking counts for the profit when not in full:
	// written off as an asset, or deductible only in part (ADR 0037).
	Note string
}

// AssetRow is one line of the Anlagenverzeichnis (ADR 0037).
type AssetRow struct {
	EntryID         int64
	Name            string
	BookedOn        string
	InUseFrom       string
	UsefulLifeYears int
	CostCents       int64
	AfACents        int64 // in the export's year
	BookValueCents  int64 // at the end of the export's year
}

// CSV writes the human-readable export: semicolon-separated with a UTF-8 BOM,
// which is what opens cleanly in a German-locale Excel without an import
// dialogue (ADR 0010). Amounts are in Austrian notation, dates stay ISO.
//
// Reversed bookings and their Stornos are included and marked, not filtered:
// the export is the record, and the record contains the corrections.
//
// Expenses booked under the Kleinunternehmerregelung are summarised apart
// and labelled: their VAT is part of the cost, and a summary that reads like
// Vorsteuer would invite a deduction that does not exist (ADR 0022).
//
// A mixed expense, used partly privately, is listed in full and summarised
// by its business share; the private share's VAT has a line of its own
// (ADR 0041).
//
// assets, when given, follow as the year's Anlagenverzeichnis.
func CSV(bookings []Booking, assets ...AssetRow) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0xEF, 0xBB, 0xBF}) // BOM

	w := csv.NewWriter(&buf)
	w.Comma = ';'

	w.Write([]string{
		"Nr", "Belegdatum", "Art", "Beschreibung", "Kontakt",
		"Brutto", "USt-Satz", "USt", "Netto",
		"Konto", "Gegenkonto", "Storno", "Storniert", "Vorsteuer abziehbar", "Betrieblicher Anteil", "Hinweis", "Belege",
	})

	for _, b := range bookings {
		art := "Einnahme"
		if b.Direction == "outgoing" {
			art = "Ausgabe"
		}
		storno := ""
		if b.IsReversal && b.ReversesEntryID != nil {
			storno = fmt.Sprintf("Storno zu %d", *b.ReversesEntryID)
		}
		storniert := ""
		if b.IsReversed {
			storniert = "ja"
		}

		deductible := ""
		if b.Direction == "outgoing" && b.VATCents > 0 {
			deductible = "ja"
			switch {
			case b.VATNotDeductible:
				deductible = "nein (Kleinunternehmer)"
			case b.mixed() && businessVAT(b.VATCents, b.BusinessPct) == 0:
				deductible = "nein (betrieblich unter 10 %)"
			case b.mixed():
				deductible = money.Format(businessVAT(b.VATCents, b.BusinessPct))
			}
		}
		share := ""
		if b.Direction == "outgoing" {
			share = fmt.Sprintf("%d%%", b.pct())
		}
		w.Write([]string{
			fmt.Sprint(b.ID), b.BookedOn, art, b.Description, b.ContactName,
			money.Format(b.AmountGrossCents), fmt.Sprintf("%d%%", b.VATRate),
			money.Format(b.VATCents), money.Format(b.NetCents),
			b.Konto, b.Gegenkonto, storno, storniert, deductible, share, b.Note, strings.Join(b.Receipts, " | "),
		})
	}

	// The VAT summary the Steuerberater actually reads first.
	w.Write(nil)
	w.Write([]string{"USt-Zusammenfassung"})
	w.Write([]string{"Art", "USt-Satz", "Netto", "USt", "Brutto"})

	type group struct {
		label     string
		direction string
		blocked   bool
		private   bool
	}
	for _, g := range []group{
		{"Einnahmen", "income", false, false},
		{"Ausgaben, betrieblicher Anteil", "outgoing", false, false},
		{"Ausgaben, Privatanteil (USt nicht abziehbar)", "outgoing", false, true},
		{"Ausgaben, Kleinunternehmer (USt nicht abziehbar)", "outgoing", true, false},
	} {
		direction, label := g.direction, g.label
		for _, rate := range money.ValidVATRates {
			var net, vat, gross int64
			var any bool
			for _, b := range bookings {
				// A reversed pair nets to zero; leaving both in the summary
				// would state VAT that was never owed (ADR 0007).
				if b.Direction != direction || b.VATRate != rate || b.IsReversal || b.IsReversed {
					continue
				}
				if direction == "outgoing" && b.VATNotDeductible != g.blocked {
					continue
				}
				bn, bv, bg := b.NetCents, b.VATCents, b.AmountGrossCents
				if direction == "outgoing" && !g.blocked {
					bizNet, bizVAT := share(b.NetCents, b.pct()), businessVAT(b.VATCents, b.pct())
					bn, bv = bizNet, bizVAT
					if g.private {
						bn, bv = b.NetCents-bizNet, b.VATCents-bizVAT
					}
					bg = bn + bv
				}
				if g.private && !b.mixed() {
					continue
				}
				any = true
				net += bn
				vat += bv
				gross += bg
			}
			if !any {
				continue
			}
			w.Write([]string{label, fmt.Sprintf("%d%%", rate),
				money.Format(net), money.Format(vat), money.Format(gross)})
		}
	}

	if len(assets) > 0 {
		w.Write(nil)
		w.Write([]string{"Anlagenverzeichnis"})
		w.Write([]string{"Nr", "Bezeichnung", "Belegdatum", "In Betrieb seit", "Nutzungsdauer (Jahre)",
			"Anschaffungskosten", "AfA des Jahres", "Restbuchwert zum Jahresende"})
		for _, a := range assets {
			w.Write([]string{fmt.Sprint(a.EntryID), a.Name, a.BookedOn, a.InUseFrom, fmt.Sprint(a.UsefulLifeYears),
				money.Format(a.CostCents), money.Format(a.AfACents), money.Format(a.BookValueCents)})
		}
	}

	w.Flush()
	return buf.Bytes()
}

// FileName is a safe download name for a period export.
func FileName(prefix string, year int, ext string) string {
	prefix = strings.ReplaceAll(prefix, " ", "-")
	return fmt.Sprintf("%s-%d.%s", prefix, year, ext)
}

func (b Booking) pct() int {
	if b.BusinessPct <= 0 {
		return 100
	}
	return b.BusinessPct
}

func (b Booking) mixed() bool { return b.pct() < 100 }

// share and businessVAT mirror store.Share and store.BusinessVAT; export
// does not import the store.
func share(cents int64, pct int) int64 { return (cents*int64(pct) + 50) / 100 }

func businessVAT(vat int64, pct int) int64 {
	if pct < 10 {
		return 0
	}
	return share(vat, pct)
}
