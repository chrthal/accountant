package store

import (
	"fmt"
	"time"
)

// The Kleinunternehmerregelung applies for a period of time, not forever
// (ADR 0022). It starts when the business starts using it, and ends at the end
// of a year, or, when revenue exceeds the tolerance band, at the booking
// that crossed the line, retroactively (§ 6 Abs 1 Z 27 UStG). Whether VAT on
// an expense is deductible therefore depends on the booking's date, and
// switching the setting off must not turn earlier, exempt years into
// deductible ones.
//
// One period is modelled: KleinunternehmerFrom (empty: from the start) to
// KleinunternehmerUntil (empty while the setting is on). Re-entering the
// regime after leaving it is not supported; its rules are their own topic
// and a case for the Steuerberater.

// KUPeriod is the time the exemption applied, both ends inclusive and
// 'YYYY-MM-DD'. An empty end is open.
type KUPeriod struct {
	From  string
	Until string
}

const (
	periodStart = "0000-01-01"
	periodEnd   = "9999-12-31"
)

// KUPeriod returns when the exemption applied, and false if it never did.
func (s Settings) KUPeriod() (KUPeriod, bool) {
	switch {
	case s.Kleinunternehmer:
		return KUPeriod{From: s.KleinunternehmerFrom}, true
	case s.KleinunternehmerUntil != "":
		return KUPeriod{From: s.KleinunternehmerFrom, Until: s.KleinunternehmerUntil}, true
	}
	return KUPeriod{}, false
}

func (p KUPeriod) bounds() (string, string) {
	from, until := p.From, p.Until
	if from == "" {
		from = periodStart
	}
	if until == "" {
		until = periodEnd
	}
	return from, until
}

// KUOn reports whether the exemption applied on date ('YYYY-MM-DD').
func (s Settings) KUOn(date string) bool {
	p, ok := s.KUPeriod()
	if !ok {
		return false
	}
	from, until := p.bounds()
	return date >= from && date <= until
}

// KU coverage of a date range, for pages that summarise a year or a period.
const (
	KUNone    = "none"
	KUPartial = "partial"
	KUFull    = "full"
)

// KUCoverage says whether the exemption applied to all, part or none of the
// range from–to (inclusive).
func (s Settings) KUCoverage(from, to string) string {
	p, ok := s.KUPeriod()
	if !ok {
		return KUNone
	}
	pf, pu := p.bounds()
	switch {
	case pu < from || pf > to:
		return KUNone
	case pf <= from && pu >= to:
		return KUFull
	}
	return KUPartial
}

// kuSQL is the condition "this booking falls into the exempt period" for the
// date column col, with its arguments. It is always valid SQL, "1 = 0" when
// the exemption never applied.
func (s Settings) kuSQL(col string) (string, []any) {
	p, ok := s.KUPeriod()
	if !ok {
		return "1 = 0", nil
	}
	from, until := p.bounds()
	return "(" + col + " >= ? AND " + col + " <= ?)", []any{from, until}
}

// validateKU checks the period on a settings save. prev is what was stored
// before: switching the exemption off needs an end date, or every earlier
// expense would suddenly count as deductible.
func validateKU(in, prev Settings) error {
	for _, d := range []string{in.KleinunternehmerFrom, in.KleinunternehmerUntil} {
		if d == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", d); err != nil {
			return fmt.Errorf("Kleinunternehmer dates must be YYYY-MM-DD")
		}
	}
	if !in.Kleinunternehmer && prev.Kleinunternehmer && in.KleinunternehmerUntil == "" {
		return fmt.Errorf("the Kleinunternehmerregelung is being switched off: enter until when it applied (Gültig bis), " +
			"so earlier expenses are not counted as deductible")
	}
	if in.KleinunternehmerFrom != "" && in.KleinunternehmerUntil != "" && in.KleinunternehmerUntil < in.KleinunternehmerFrom {
		return fmt.Errorf("the Kleinunternehmer end date is before its start date")
	}
	return nil
}
