package server

import (
	"testing"

	"github.com/chrthal/accountant/internal/report"
	"github.com/chrthal/accountant/internal/store"
)

func TestNiceScaleCoversThePeakWithRoundSteps(t *testing.T) {
	for _, tc := range []struct{ peak int64 }{
		{0}, {1}, {123_456}, {4_000_000}, {4_100_000}, {987_654_321},
	} {
		top, step := niceScale(tc.peak)
		if top < tc.peak {
			t.Errorf("niceScale(%d) top %d is below the peak", tc.peak, top)
		}
		if top%step != 0 || top/step > 5 || top/step < 1 {
			t.Errorf("niceScale(%d) = %d/%d: not 1–5 whole steps", tc.peak, top, step)
		}
	}
}

func TestShortEuro(t *testing.T) {
	for cents, want := range map[int64]string{0: "0", 75_000: "750", 250_000: "2,5k", 1_200_000: "12k"} {
		if got := shortEuro(cents); got != want {
			t.Errorf("shortEuro(%d) = %q, want %q", cents, got, want)
		}
	}
}

func TestBuildChartScalesToThePeak(t *testing.T) {
	var months [12]store.MonthTotals
	months[2].IncomeGrossCents = 400_000
	months[2].OutgoingGrossCents = 100_000
	months[5].OutgoingGrossCents = 1 // tiny, but must still be visible

	c := buildChart(months, 3)
	if c.Empty {
		t.Fatal("chart with bookings reported as empty")
	}
	if len(c.Bars) != 12 {
		t.Fatalf("bars = %d, want 12", len(c.Bars))
	}
	mar := c.Bars[2]
	if !mar.Current || c.Bars[0].Current {
		t.Error("the current month is not the one highlighted")
	}
	if mar.InH <= mar.OutH*3.5 || mar.InH > c.Bottom {
		t.Errorf("March bars out of proportion: in %.1f out %.1f", mar.InH, mar.OutH)
	}
	if c.Bars[5].OutH < 1.5 {
		t.Error("a non-zero month rendered as nothing")
	}
	if c.Bars[0].InH != 0 {
		t.Error("an empty month drew a bar")
	}
}

func TestMeterMarksBothLimits(t *testing.T) {
	m := buildMeter(report.Threshold{RevenueCents: report.ThresholdCents, Band: report.BandUnder})
	if m.FillPct != m.ThresholdPct {
		t.Errorf("revenue at the limit fills %.1f%%, the limit mark is at %.1f%%", m.FillPct, m.ThresholdPct)
	}
	if !(m.ThresholdPct < m.TolerancePct && m.TolerancePct < 100) {
		t.Errorf("marks out of order: %.1f / %.1f", m.ThresholdPct, m.TolerancePct)
	}
	if over := buildMeter(report.Threshold{RevenueCents: 50 * report.ToleranceCents}); over.FillPct != 100 {
		t.Errorf("a far exceeded year overflows the meter: %.1f%%", over.FillPct)
	}
}
