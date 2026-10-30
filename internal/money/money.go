// Package money handles amounts as integer cents (ADR 0007) and the
// Austrian-format parsing and rendering the UI needs.
//
// Nothing here uses floats: a booking's numbers must be exact and reproducible.
package money

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ValidVATRates are the rates the app accepts, in percent. § 6 Abs 1 Z 27 UStG
// Kleinunternehmer bookings use 0. The reduced-rate *categories* shift
// occasionally — that is a question of which rate applies to what, not of which
// rates exist, and is not decided here.
var ValidVATRates = []int{0, 10, 13, 20}

// IsValidVATRate reports whether rate is one of ValidVATRates.
func IsValidVATRate(rate int) bool {
	for _, r := range ValidVATRates {
		if r == rate {
			return true
		}
	}
	return false
}

// ErrInvalidAmount is returned by Parse for anything that is not a positive
// amount of euros.
var ErrInvalidAmount = errors.New("not a valid amount")

// Parse reads a user-typed euro amount and returns whole cents.
//
// Austrian convention is "1.234,56" — dot groups thousands, comma is the
// decimal separator — but people also type "1234.56" and "1234,56", so whichever
// of . or , appears last is taken as the decimal separator and the other as a
// group separator.
func Parse(s string) (int64, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "€", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, " ", "") // non-breaking space
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, ErrInvalidAmount
	}

	lastDot, lastComma := strings.LastIndex(s, "."), strings.LastIndex(s, ",")
	switch {
	case lastDot >= 0 && lastComma >= 0:
		if lastComma > lastDot { // 1.234,56
			s = strings.ReplaceAll(s, ".", "")
			s = strings.Replace(s, ",", ".", 1)
		} else { // 1,234.56
			s = strings.ReplaceAll(s, ",", "")
		}
	case lastComma >= 0:
		s = strings.Replace(s, ",", ".", 1)
	case lastDot >= 0:
		// Only dots. "1.234.567" is grouped thousands; "1234.56" is decimal.
		// The tell is what follows the final dot: exactly three digits (or more
		// than one dot at all) means grouping, since nobody writes three
		// decimal places for euros.
		if strings.Count(s, ".") > 1 || len(s)-lastDot-1 == 3 {
			s = strings.ReplaceAll(s, ".", "")
		}
	}

	whole, frac, hasFrac := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	euros, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || euros < 0 {
		return 0, ErrInvalidAmount
	}

	var cents int64
	if hasFrac {
		switch len(frac) {
		case 0:
		case 1:
			c, err := strconv.ParseInt(frac, 10, 64)
			if err != nil {
				return 0, ErrInvalidAmount
			}
			cents = c * 10
		case 2:
			c, err := strconv.ParseInt(frac, 10, 64)
			if err != nil {
				return 0, ErrInvalidAmount
			}
			cents = c
		default:
			// More precision than a cent is a typo, not a rounding request.
			return 0, fmt.Errorf("%w: more than two decimal places", ErrInvalidAmount)
		}
	}

	return euros*100 + cents, nil
}

// Format renders cents in Austrian notation without a currency symbol:
// 123456 -> "1.234,56". Negative amounts keep their sign.
func Format(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}

	whole := strconv.FormatInt(cents/100, 10)
	var grouped strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			grouped.WriteByte('.')
		}
		grouped.WriteRune(r)
	}

	out := fmt.Sprintf("%s,%02d", grouped.String(), cents%100)
	if neg {
		return "-" + out
	}
	return out
}

// SplitVAT divides a gross amount into its VAT and net parts:
//
//	vat = round(gross × rate ÷ (100 + rate)), net = gross − vat
//
// Rounding is half-up on the cent, done in integers. The result is stored with
// the booking (ADR 0007) so its numbers never move afterwards.
func SplitVAT(grossCents int64, rate int) (vatCents, netCents int64) {
	if rate == 0 {
		return 0, grossCents
	}
	num := grossCents * int64(rate)
	den := int64(100 + rate)
	vat := (num + den/2) / den // half-up; all values are non-negative here
	return vat, grossCents - vat
}

// VATFromNet returns the VAT on a net amount: round(net × rate ÷ 100), half-up.
//
// This is the invoice direction. SplitVAT is the booking direction, where the
// gross is what the user knows. The two are not inverses at the cent level, so
// each is used where its input is the authoritative figure.
func VATFromNet(netCents int64, rate int) int64 {
	if rate == 0 {
		return 0
	}
	num := netCents * int64(rate)
	return (num + 50) / 100 // half-up; net is never negative here
}

// LineNet multiplies a unit price by a quantity given in thousandths, so that
// 1,5 hours is 1500 and no float ever touches an amount. Rounds half-up.
func LineNet(unitPriceCents, quantityMilli int64) int64 {
	return (unitPriceCents*quantityMilli + 500) / 1000
}

// ParseQuantity reads a user-typed quantity and returns thousandths.
// "1,5" -> 1500, "2" -> 2000, "0,25" -> 250.
func ParseQuantity(s string) (int64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	if s == "" {
		return 0, fmt.Errorf("%w: quantity is empty", ErrInvalidAmount)
	}
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w < 0 {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
	}
	if len(frac) > 3 {
		return 0, fmt.Errorf("%w: more than three decimal places", ErrInvalidAmount)
	}
	for len(frac) < 3 {
		frac += "0"
	}
	var f int64
	if frac != "000" {
		if f, err = strconv.ParseInt(frac, 10, 64); err != nil {
			return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
		}
	}
	return w*1000 + f, nil
}

// FormatQuantity renders thousandths back for display, trimming trailing zeros:
// 1500 -> "1,5", 2000 -> "2", 250 -> "0,25".
func FormatQuantity(milli int64) string {
	whole, frac := milli/1000, milli%1000
	if frac == 0 {
		return strconv.FormatInt(whole, 10)
	}
	s := fmt.Sprintf("%03d", frac)
	s = strings.TrimRight(s, "0")
	return strconv.FormatInt(whole, 10) + "," + s
}
