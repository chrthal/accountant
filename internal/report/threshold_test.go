package report_test

import (
	"testing"

	"github.com/chrthal/accountant/internal/report"
)

func rev(id int64, cents int64) report.Revenue {
	return report.Revenue{EntryID: id, BookedOn: "2026-01-01", Description: "Honorar", GrossCents: cents}
}

func TestBands(t *testing.T) {
	cases := []struct {
		name  string
		cents int64
		want  report.Band
	}{
		{"well under", 1_000_000, report.BandUnder},
		{"just under the limit", 5_499_999, report.BandUnder},
		{"exactly at the limit", 5_500_000, report.BandUnder},
		{"one cent over the limit", 5_500_001, report.BandTolerance},
		{"exactly at the ceiling", 6_050_000, report.BandTolerance},
		{"one cent over the ceiling", 6_050_001, report.BandExceeded},
	}
	for _, c := range cases {
		got := report.ComputeThreshold(2026, []report.Revenue{rev(1, c.cents)})
		if got.Band != c.want {
			t.Errorf("%s (%d cents): band = %q, want %q", c.name, c.cents, got.Band, c.want)
		}
	}
}

// The retroactive consequence of passing the ceiling attaches to a specific
// booking, so the tracker has to name it rather than only report a total.
func TestNamesTheBookingThatCrossedEachLine(t *testing.T) {
	got := report.ComputeThreshold(2026, []report.Revenue{
		rev(1, 3_000_000), // 30.000
		rev(2, 2_600_000), // 56.000 — crosses €55.000
		rev(3, 400_000),   // 60.000 — still inside the band
		rev(4, 200_000),   // 62.000 — crosses €60.500
	})

	if got.Band != report.BandExceeded {
		t.Fatalf("band = %q, want exceeded", got.Band)
	}
	if got.CrossedThresholdAt == nil || got.CrossedThresholdAt.EntryID != 2 {
		t.Errorf("threshold crossing = %v, want entry 2", got.CrossedThresholdAt)
	}
	if got.CrossedToleranceAt == nil || got.CrossedToleranceAt.EntryID != 4 {
		t.Errorf("tolerance crossing = %v, want entry 4", got.CrossedToleranceAt)
	}
	if got.RevenueCents != 6_200_000 {
		t.Errorf("revenue = %d, want 6200000", got.RevenueCents)
	}
	if got.RemainingToThresholdCents != 0 || got.RemainingToToleranceCents != 0 {
		t.Error("remaining amounts should be zero once both lines are passed")
	}
}

func TestRemainingAmounts(t *testing.T) {
	got := report.ComputeThreshold(2026, []report.Revenue{rev(1, 5_000_000)})
	if got.RemainingToThresholdCents != 500_000 {
		t.Errorf("remaining to threshold = %d, want 500000", got.RemainingToThresholdCents)
	}
	if got.RemainingToToleranceCents != 1_050_000 {
		t.Errorf("remaining to tolerance = %d, want 1050000", got.RemainingToToleranceCents)
	}
	if got.PercentOfThreshold() != 90 {
		t.Errorf("percent = %d, want 90", got.PercentOfThreshold())
	}
}

func TestEmptyYear(t *testing.T) {
	got := report.ComputeThreshold(2026, nil)
	if got.Band != report.BandUnder || got.RevenueCents != 0 {
		t.Errorf("empty year = %+v, want an untouched under-limit result", got)
	}
	if got.CrossedThresholdAt != nil || got.CrossedToleranceAt != nil {
		t.Error("empty year reported a crossing")
	}
	if got.RemainingToThresholdCents != report.ThresholdCents {
		t.Errorf("remaining = %d, want the full threshold", got.RemainingToThresholdCents)
	}
}

// Only the first crossing counts: later bookings must not move the marker.
func TestCrossingIsRecordedOnce(t *testing.T) {
	got := report.ComputeThreshold(2026, []report.Revenue{
		rev(1, 5_600_000), // crosses immediately
		rev(2, 100_000),
		rev(3, 100_000),
	})
	if got.CrossedThresholdAt == nil || got.CrossedThresholdAt.EntryID != 1 {
		t.Errorf("threshold crossing = %v, want the first booking", got.CrossedThresholdAt)
	}
}
