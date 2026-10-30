package report

// The SVS Kleinunternehmer exemption (Ausnahme von der Pflichtversicherung
// in der Pensions- und Krankenversicherung nach GSVG) has two yearly limits:
// the year's Einkünfte from self-employment, and its revenue. Accident
// insurance stays in force either way (ADR 0023).
//
// The values are as published by the SVS
// (svs.at, contentid=10007.846813, read 2026-10-15). They change every year,
// and a year that is not listed here shows "not on file" instead of a
// guessed limit.

// SVSLimits are one year's limits, in cents.
type SVSLimits struct {
	IncomeCents  int64 // Einkünfte
	RevenueCents int64 // Umsatz
}

var svsLimits = map[int]SVSLimits{
	2024: {IncomeCents: 622128, RevenueCents: 3_500_000},
	2025: {IncomeCents: 661320, RevenueCents: 5_500_000},
	2026: {IncomeCents: 661320, RevenueCents: 5_500_000},
}

// SVSLimitsFor returns the limits for a year, and false when none are on file.
func SVSLimitsFor(year int) (SVSLimits, bool) {
	l, ok := svsLimits[year]
	return l, ok
}

// SVS is where a year stands against the SVS limits.
type SVS struct {
	Year      int
	Limits    SVSLimits
	Known     bool  // limits are on file for the year
	Income    int64 // approximation of the Einkünfte, see ComputeSVS
	Revenue   int64
	IncomeOK  bool // at or below the limit
	RevenueOK bool
}

// Within reports whether both figures are inside their limits.
func (s SVS) Within() bool { return s.IncomeOK && s.RevenueOK }

// IncomePercent and RevenuePercent are how far through each limit the year is.
func (s SVS) IncomePercent() int  { return percent(s.Income, s.Limits.IncomeCents) }
func (s SVS) RevenuePercent() int { return percent(s.Revenue, s.Limits.RevenueCents) }

// IncomeLeftCents and RevenueLeftCents are what remains below each limit.
func (s SVS) IncomeLeftCents() int64  { return max(s.Limits.IncomeCents-s.Income, 0) }
func (s SVS) RevenueLeftCents() int64 { return max(s.Limits.RevenueCents-s.Revenue, 0) }

func percent(v, limit int64) int {
	if limit <= 0 || v <= 0 {
		return 0
	}
	return int(v * 100 / limit)
}

// ComputeSVS compares a year's figures with the SVS limits. income is the
// profit the books show, which only approximates the Einkünfte the SVS uses:
// those come from the income-tax assessment, after depreciation (AfA) and the
// Gewinnfreibetrag, which the books do not know. revenue is the same
// effective income the USt threshold uses (ComputeThreshold).
func ComputeSVS(year int, revenueCents, incomeCents int64) SVS {
	l, ok := SVSLimitsFor(year)
	s := SVS{Year: year, Limits: l, Known: ok, Income: incomeCents, Revenue: revenueCents}
	s.IncomeOK = !ok || incomeCents <= l.IncomeCents
	s.RevenueOK = !ok || revenueCents <= l.RevenueCents
	return s
}

// IncomeFill and RevenueFill are the bar widths in percent, capped at 100.
func (s SVS) IncomeFill() int  { return min(s.IncomePercent(), 100) }
func (s SVS) RevenueFill() int { return min(s.RevenuePercent(), 100) }
