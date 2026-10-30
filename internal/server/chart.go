package server

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/chrthal/accountant/internal/report"
	"github.com/chrthal/accountant/internal/store"
)

// The overview chart is server-rendered SVG: twelve months, income and
// outgoings side by side. This package computes the geometry and the template
// only draws it, so there is no charting library, no script, and nothing that
// a CSP has to allow (ADR 0013).

const (
	chartW      = 640.0
	chartH      = 220.0
	chartLeft   = 46.0 // room for the y-axis labels
	chartBottom = 22.0 // room for the month labels
	chartTop    = 8.0
)

var monthShort = [12]string{"Jän", "Feb", "Mär", "Apr", "Mai", "Jun", "Jul", "Aug", "Sep", "Okt", "Nov", "Dez"}

type chartBar struct {
	Label            string
	LabelX           float64
	BandX, BandW     float64
	InX, OutX, BarW  float64
	InY, InH         float64
	OutY, OutH       float64
	Income, Outgoing int64
	Current          bool
}

type chartTick struct {
	Y     float64
	Label string
}

type chart struct {
	W, H   float64
	Left   float64
	AxisX  float64 // right edge of the y-axis labels
	Bottom float64 // y of the baseline
	Bars   []chartBar
	Ticks  []chartTick
	Empty  bool
}

// buildChart lays out a year's months. currentMonth (1–12, or 0 for none)
// is highlighted, so the running month is easy to find.
func buildChart(months [12]store.MonthTotals, currentMonth int) chart {
	var peak int64
	for _, m := range months {
		peak = max(peak, m.IncomeGrossCents, m.OutgoingGrossCents)
	}
	c := chart{W: chartW, H: chartH, Left: chartLeft, AxisX: chartLeft - 6, Bottom: chartH - chartBottom, Empty: peak == 0}

	top, step := niceScale(peak)
	plotH := c.Bottom - chartTop
	scale := func(cents int64) float64 { return float64(cents) / float64(top) * plotH }

	for v := int64(0); v <= top; v += step {
		c.Ticks = append(c.Ticks, chartTick{Y: round1(c.Bottom - scale(v)), Label: shortEuro(v)})
	}

	band := (chartW - chartLeft) / 12
	barW := math.Min(14, band*0.3)
	gap := 3.0
	for i, m := range months {
		x := chartLeft + float64(i)*band
		mid := x + band/2
		inH, outH := scale(m.IncomeGrossCents), scale(m.OutgoingGrossCents)
		// A non-zero month gets at least a hairline, so "a little" never
		// looks like "nothing".
		if m.IncomeGrossCents > 0 {
			inH = math.Max(inH, 1.5)
		}
		if m.OutgoingGrossCents > 0 {
			outH = math.Max(outH, 1.5)
		}
		c.Bars = append(c.Bars, chartBar{
			Label: monthShort[i], LabelX: round1(mid),
			BandX: round1(x + 2), BandW: round1(band - 4),
			InX: round1(mid - gap/2 - barW), OutX: round1(mid + gap/2), BarW: round1(barW),
			InY: round1(c.Bottom - inH), InH: round1(inH),
			OutY: round1(c.Bottom - outH), OutH: round1(outH),
			Income: m.IncomeGrossCents, Outgoing: m.OutgoingGrossCents,
			Current: i+1 == currentMonth,
		})
	}
	return c
}

// niceScale picks an axis top and step (in cents) so that four or five round
// gridlines cover peak: 1, 2, 2.5 or 5 times a power of ten, in euros.
func niceScale(peakCents int64) (top, step int64) {
	if peakCents <= 0 {
		return 100_000, 25_000 // €1.000 in steps of €250: an empty year still has an axis
	}
	raw := float64(peakCents) / 4
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, f := range []float64{1, 2, 2.5, 5, 10} {
		if s := f * mag; s >= raw {
			step = int64(math.Ceil(s))
			break
		}
	}
	// Float rounding in Pow/Log10 must never leave step at zero, since the
	// tick loop steps by it.
	if step <= 0 {
		step = max(int64(math.Ceil(10*mag)), 1)
	}
	top = step * int64(math.Ceil(float64(peakCents)/float64(step)))
	return top, step
}

// shortEuro renders an axis label: "0", "750", "2,5k", "12k".
func shortEuro(cents int64) string {
	euros := float64(cents) / 100
	if euros < 1000 {
		return strconv.FormatFloat(euros, 'f', -1, 64)
	}
	s := strconv.FormatFloat(euros/1000, 'f', 1, 64)
	s = strings.TrimSuffix(s, ".0")
	return strings.ReplaceAll(s, ".", ",") + "k"
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

// meter is the Kleinunternehmer gauge: revenue against the €55.000 limit and
// the €60.500 tolerance ceiling. The scale runs a little past the ceiling so
// an exceeded year still shows how far past it went, up to a point.
type meter struct {
	FillPct      float64
	ThresholdPct float64
	TolerancePct float64
	Band         report.Band
}

func buildMeter(t report.Threshold) meter {
	scale := float64(report.ToleranceCents) * 1.1
	pct := func(c int64) float64 { return round1(math.Min(100, float64(c)/scale*100)) }
	return meter{
		FillPct:      pct(t.RevenueCents),
		ThresholdPct: pct(report.ThresholdCents),
		TolerancePct: pct(report.ToleranceCents),
		Band:         t.Band,
	}
}

// monthName is the long month name for 1–12, in the UI language.
func (s *Server) monthName(m int) string {
	names := [12]string{"Jänner", "Februar", "März", "April", "Mai", "Juni", "Juli",
		"August", "September", "Oktober", "November", "Dezember"}
	if m < 1 || m > 12 {
		return ""
	}
	return s.T(names[m-1])
}

// formatDate renders 'YYYY-MM-DD' in layout. Anything unparseable is
// returned unchanged rather than hidden.
func formatDate(iso, layout string) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return t.Format(layout)
}
