// Package report computes the figures the app shows and exports.
//
// This is arithmetic, not tax advice (ADR 0010). The Kleinunternehmer tracker
// reports where the year stands against the limits and which booking crossed
// them; deciding what follows from that is the Steuerberater's job.
package report

// Kleinunternehmer limits, § 6 Abs 1 Z 27 UStG, in cents.
//
// The limit has been €55.000 gross since 1.1.2025 (previously €35.000 net),
// with a 10 % tolerance band on top.
const (
	ThresholdCents = 5_500_000 // €55.000
	ToleranceCents = 6_050_000 // €60.500 — the 10 % band
)

// Band is where a year's revenue falls relative to the limits.
type Band string

const (
	// BandUnder: below €55.000 — the exemption applies unchanged.
	BandUnder Band = "under"
	// BandTolerance: above €55.000 but within the 10 % band. The exemption
	// holds through year-end and drops the following year.
	BandTolerance Band = "tolerance"
	// BandExceeded: above €60.500. The exemption ends immediately, retroactive
	// to the booking that crossed the ceiling.
	BandExceeded Band = "exceeded"
)

// Revenue is one booking's contribution to the year's revenue, in the order it
// was booked.
type Revenue struct {
	EntryID     int64
	BookedOn    string
	Description string
	GrossCents  int64
}

// Threshold is the tracker's result.
type Threshold struct {
	Year                      int
	RevenueCents              int64
	Band                      Band
	RemainingToThresholdCents int64 // 0 once the limit is passed
	RemainingToToleranceCents int64 // 0 once the ceiling is passed

	// CrossedThresholdAt and CrossedToleranceAt name the booking at which the
	// running total passed each line. The tolerance crossing is the one the
	// retroactive consequence attaches to, so it has to be identifiable rather
	// than merely implied by a total.
	CrossedThresholdAt *Revenue
	CrossedToleranceAt *Revenue
}

// PercentOfThreshold is how far through the €55.000 limit the year is, for a
// progress display. It can exceed 100.
func (t Threshold) PercentOfThreshold() int {
	if ThresholdCents == 0 {
		return 0
	}
	return int(t.RevenueCents * 100 / ThresholdCents)
}

// ComputeThreshold walks the year's revenues in booking order and reports where
// it stands.
//
// The caller supplies only *effective* income — a reversed booking and its
// Storno both drop out (ADR 0007, ADR 0010) — because a correction must not
// inflate the figure the exemption depends on.
func ComputeThreshold(year int, revenues []Revenue) Threshold {
	t := Threshold{Year: year, Band: BandUnder}

	for i := range revenues {
		before := t.RevenueCents
		t.RevenueCents += revenues[i].GrossCents

		if before <= ThresholdCents && t.RevenueCents > ThresholdCents && t.CrossedThresholdAt == nil {
			t.CrossedThresholdAt = &revenues[i]
		}
		if before <= ToleranceCents && t.RevenueCents > ToleranceCents && t.CrossedToleranceAt == nil {
			t.CrossedToleranceAt = &revenues[i]
		}
	}

	switch {
	case t.RevenueCents > ToleranceCents:
		t.Band = BandExceeded
	case t.RevenueCents > ThresholdCents:
		t.Band = BandTolerance
	}

	t.RemainingToThresholdCents = max(0, ThresholdCents-t.RevenueCents)
	t.RemainingToToleranceCents = max(0, ToleranceCents-t.RevenueCents)
	return t
}
