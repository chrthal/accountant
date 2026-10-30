package report

import "testing"

func TestSVSLimits(t *testing.T) {
	// Published by the SVS for 2025 and 2026: Einkünfte 6.613,20 €, Umsatz 55.000 €.
	for _, y := range []int{2025, 2026} {
		if l, ok := SVSLimitsFor(y); !ok || l.IncomeCents != 661320 || l.RevenueCents != 5_500_000 {
			t.Errorf("%d limits = %+v %v", y, l, ok)
		}
	}
	if l, _ := SVSLimitsFor(2024); l.RevenueCents != 3_500_000 {
		t.Errorf("2024 revenue limit = %d, want the old 35.000 €", l.RevenueCents)
	}
	if _, ok := SVSLimitsFor(2030); ok {
		t.Error("a year without published limits has some")
	}

	s := ComputeSVS(2026, 4_000_000, 661320)
	if !s.Known || !s.Within() || s.IncomeLeftCents() != 0 || s.RevenuePercent() != 72 {
		t.Errorf("at the income limit: %+v", s)
	}
	s = ComputeSVS(2026, 4_000_000, 661321)
	if s.IncomeOK || s.Within() || !s.RevenueOK {
		t.Errorf("one cent over the income limit: %+v", s)
	}
	if s := ComputeSVS(2026, 5_500_001, 0); s.RevenueOK {
		t.Error("revenue over 55.000 € is within")
	}
	if s := ComputeSVS(2030, 9_999_999, 9_999_999); s.Known || !s.Within() {
		t.Errorf("an unknown year must not report an overshoot: %+v", s)
	}
}
