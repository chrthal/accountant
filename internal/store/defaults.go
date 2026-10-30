package store

import (
	"context"
	"errors"
)

func vat(rate int) *int { return &rate }

// DefaultCategories are the categories a new installation starts with
// (ADR 0027): what any one-person business or small company in Austria
// sorts its bank lines into, whatever its trade. Keywords name nationwide
// payees only, and only names that mean that payee and nothing else.
//
// A default VAT rate is set only where the rate is the same on every
// receipt. Where it varies (travel at 10 % or 13 %, rent, foreign software
// and advertising under reverse charge), it is left open, and the standard
// rate from the settings applies until a booking or the user sets one.
// Interest at 0 % is the exempt case (§ 6 Abs 1 Z 8 UStG).
// Keywords never repeat across categories: a line that hits two categories
// gets none.
var DefaultCategories = []Category{
	{Name: "Erlöse Dienstleistungen", Direction: "income"},
	{Name: "Erlöse Warenverkauf", Direction: "income"},
	{Name: "Sonstige Erträge", Direction: "income", Keywords: "zinsen, habenzinsen", DefaultVATRate: vat(0)},

	{Name: "Wareneinkauf & Material", Direction: "outgoing"},
	{Name: "Fremdleistungen", Direction: "outgoing"},
	{Name: "Büromaterial", Direction: "outgoing", Keywords: "libro, pagro, viking", DefaultVATRate: vat(20)},
	{Name: "Software & IT-Dienste", Direction: "outgoing", Keywords: "adobe, dropbox"},
	{Name: "Hardware & GWG", Direction: "outgoing", Keywords: "mediamarkt, media markt, saturn, conrad, cyberport", DefaultVATRate: vat(20)},
	{Name: "Telefon & Internet", Direction: "outgoing", Keywords: "a1 telekom, magenta, hutchison, spusu, hofer telekom, yesss", DefaultVATRate: vat(20)},
	{Name: "Strom, Gas & Energie", Direction: "outgoing", Keywords: "verbund, wien energie, energie steiermark, evn, kelag, energie ag, salzburg ag, tiwag, linz ag", DefaultVATRate: vat(20)},
	{Name: "Miete & Betriebskosten", Direction: "outgoing", Keywords: "miete"},
	{Name: "Instandhaltung & Reparatur", Direction: "outgoing"},
	// The Vorsteuer on a passenger car is not deductible in Austria (§ 12 Abs 2
	// Z 2 lit b UStG), so its receipts are booked gross at 0 %. Electric cars
	// and trucks differ; the user changes the rate for those.
	{Name: "Kfz-Kosten (PKW)", Direction: "outgoing", Keywords: "omv, bp, shell, eni, avanti, jet, turmoel, oeamtc, arboe, asfinag", DefaultVATRate: vat(0)},
	{Name: "Reisekosten", Direction: "outgoing", Keywords: "oebb, westbahn, austrian airlines"},
	// Business meals with an advertising purpose count half (§ 20 Abs 1 Z 3
	// EStG, ADR 0037). Restaurants print 10 % or 20 %, so no default rate.
	{Name: "Bewirtung (Geschäftsessen)", Direction: "outgoing", DeductiblePct: 50},
	{Name: "Werbung & Marketing", Direction: "outgoing", Keywords: "google ads, meta platforms"},
	{Name: "Fortbildung & Fachliteratur", Direction: "outgoing"},
	{Name: "Porto & Versand", Direction: "outgoing", Keywords: "oesterreichische post, post ag, dhl, dpd, gls, ups"},
	{Name: "Steuer- & Rechtsberatung", Direction: "outgoing", Keywords: "steuerberat, rechtsanwalt, notar", DefaultVATRate: vat(20)},
	{Name: "Versicherungen", Direction: "outgoing", Keywords: "uniqa, generali, allianz, wiener staedtische, grawe, donau versicherung", DefaultVATRate: vat(0)},
	{Name: "Sozialversicherung (SVS)", Direction: "outgoing", Keywords: "svs, sozialversicherung", DefaultVATRate: vat(0)},
	{Name: "Beiträge & Gebühren", Direction: "outgoing", Keywords: "wko, wirtschaftskammer, kammerumlage, grundumlage", DefaultVATRate: vat(0)},
	{Name: "Bankspesen", Direction: "outgoing", Keywords: "kontofuehrung, kontoentgelt, kartenentgelt, buchungsentgelt, spesen", DefaultVATRate: vat(0)},
	{Name: "Sonstige Ausgaben", Direction: "outgoing"},
}

// SeedDefaultCategories adds DefaultCategories to a new installation: only
// when no table that backups cover has a single row. A database that holds
// anything, including categories the user deleted on purpose, is left as it
// is. It reports whether it added them.
//
// It runs when the server starts, not as a migration: a restore migrates
// its target first and needs it empty.
func (s *Store) SeedDefaultCategories(ctx context.Context) (bool, error) {
	if err := s.requireEmpty(ctx); errors.Is(err, ErrNotEmpty) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	for _, c := range DefaultCategories {
		if err := c.validate(); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, s.Rebind(`INSERT INTO categories (name, direction, default_konto,
			default_gegenkonto, keywords, default_vat_rate, deductible_pct) VALUES (?, ?, '', '', ?, ?, ?)`),
			c.Name, c.Direction, normaliseKeywords(c.Keywords), c.DefaultVATRate, c.Deductible()); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}
