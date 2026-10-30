package money_test

import (
	"testing"

	"github.com/chrthal/accountant/internal/money"
)

func TestParse(t *testing.T) {
	ok := map[string]int64{
		"1234,56":   123456,
		"1.234,56":  123456,
		"1,234.56":  123456,
		"1234.56":   123456,
		"1234":      123400,
		"0,05":      5,
		"0,5":       50,
		",5":        50,
		"12,00":     1200,
		" 99,99 ":   9999,
		"€ 10,00":   1000,
		"1.234":     123400, // ambiguous by design: Austrian grouping wins
		"1.234.567": 123456700,
	}
	for in, want := range ok {
		got, err := money.Parse(in)
		if err != nil {
			t.Errorf("Parse(%q): unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Parse(%q) = %d, want %d", in, got, want)
		}
	}

	// Three decimals is a typo, not a request to round — a bookkeeping tool
	// must not silently reinterpret an amount.
	for _, in := range []string{"", "abc", "-5,00", "1,234", "1.2.3,4,5"} {
		if got, err := money.Parse(in); err == nil {
			t.Errorf("Parse(%q) = %d, want an error", in, got)
		}
	}
}

func TestFormat(t *testing.T) {
	cases := map[int64]string{
		0:         "0,00",
		5:         "0,05",
		50:        "0,50",
		1200:      "12,00",
		123456:    "1.234,56",
		123456700: "1.234.567,00",
		-123456:   "-1.234,56",
	}
	for in, want := range cases {
		if got := money.Format(in); got != want {
			t.Errorf("Format(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestParseFormatRoundTrip(t *testing.T) {
	for _, cents := range []int64{0, 1, 99, 100, 12345, 999999999} {
		got, err := money.Parse(money.Format(cents))
		if err != nil {
			t.Fatalf("Parse(Format(%d)): %v", cents, err)
		}
		if got != cents {
			t.Errorf("round trip of %d gave %d", cents, got)
		}
	}
}

func TestSplitVAT(t *testing.T) {
	cases := []struct {
		gross    int64
		rate     int
		vat, net int64
	}{
		{12000, 20, 2000, 10000},           // 100,00 net + 20% = 120,00 gross
		{11000, 10, 1000, 10000},           // reduced rate
		{11300, 13, 1300, 10000},           // 13% rate
		{10000, 0, 0, 10000},               // Kleinunternehmer: no VAT
		{100, 20, 17, 83},                  // 1,00 gross -> 0,1666… rounds half-up to 0,17
		{1, 20, 0, 1},                      // a single cent: VAT rounds to zero
		{99999999, 20, 16666667, 83333332}, // exactly .5 cents of VAT: rounds up
	}
	for _, c := range cases {
		vat, net := money.SplitVAT(c.gross, c.rate)
		if vat != c.vat || net != c.net {
			t.Errorf("SplitVAT(%d, %d) = (%d, %d), want (%d, %d)",
				c.gross, c.rate, vat, net, c.vat, c.net)
		}
		// The invariant that actually matters: the parts must reconstitute the
		// whole exactly, at every rate and amount.
		if vat+net != c.gross {
			t.Errorf("SplitVAT(%d, %d): %d + %d != %d", c.gross, c.rate, vat, net, c.gross)
		}
	}
}

// Whatever the amount and rate, the split must never lose or invent a cent.
func TestSplitVATNeverLosesACent(t *testing.T) {
	for _, rate := range money.ValidVATRates {
		for gross := int64(1); gross <= 5000; gross++ {
			vat, net := money.SplitVAT(gross, rate)
			if vat+net != gross {
				t.Fatalf("SplitVAT(%d, %d) = (%d, %d): parts do not sum to the whole",
					gross, rate, vat, net)
			}
			if vat < 0 || net < 0 {
				t.Fatalf("SplitVAT(%d, %d) produced a negative part: (%d, %d)",
					gross, rate, vat, net)
			}
		}
	}
}
