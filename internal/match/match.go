// Package match holds the rules that link bank lines to invoices and to
// earlier bookings (ADR 0020). They are deliberate, explainable and
// deterministic: every suggestion says why it was made, and nothing here
// books or matches anything by itself.
package match

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Invoice is what the rules need to know about an open invoice.
type Invoice struct {
	ID            int64
	Number        string // "2026-0001"
	RecipientName string
	OpenCents     int64
}

// InvoiceHint is one suggested invoice for an incoming transfer.
type InvoiceHint struct {
	Invoice
	NumberFound bool // the invoice number appears in the transfer text
	AmountEqual bool // the transfer is exactly the open amount
	NameMatches bool // the sender resembles the invoice recipient
	// Partial: the transfer names the invoice and pays less than is open,
	// a part payment (ADR 0036). AmountCents is what it pays.
	Partial     bool
	AmountCents int64
}

// Sure reports whether the hint is unambiguous enough for the bulk action:
// the transfer names the invoice and pays exactly what is open. Name alone
// or amount alone can coincide, but an invoice number with the right amount
// does not.
func (h InvoiceHint) Sure() bool { return h.NumberFound && h.AmountEqual }

// Bookable reports whether booking the payment from this hint is possible:
// the exact open amount, or a part payment of an invoice the transfer names
// (ADR 0036). More than is open never is.
func (h InvoiceHint) Bookable() bool { return h.AmountEqual || h.Partial }

// Reasons explains the hint in the words the bank page shows.
func (h InvoiceHint) Reasons() []string {
	var r []string
	if h.NumberFound {
		r = append(r, "Rechnungsnummer im Text")
	}
	if h.AmountEqual {
		r = append(r, "Betrag stimmt")
	}
	if h.NameMatches {
		r = append(r, "Name passt")
	}
	return r
}

func (h InvoiceHint) score() int {
	s := 0
	if h.NumberFound {
		s += 4
	}
	if h.AmountEqual {
		s += 2
	}
	if h.NameMatches {
		s++
	}
	return s
}

// Invoices ranks open invoices for an incoming transfer of amountCents, sent
// by counterparty with the given text. A hint needs the right amount, or the
// invoice number in the text. A number with a different amount is still shown,
// because it points at a short or partial payment the user must look at,
// even though it cannot be booked in one click.
func Invoices(text, counterparty string, amountCents int64, open []Invoice) []InvoiceHint {
	numbers := numbersIn(text + " " + counterparty)
	var out []InvoiceHint
	for _, inv := range open {
		h := InvoiceHint{
			Invoice:     inv,
			NumberFound: numbers[digits(inv.Number)] && digits(inv.Number) != "",
			AmountEqual: inv.OpenCents == amountCents,
			NameMatches: SimilarName(counterparty, inv.RecipientName),
			AmountCents: amountCents,
		}
		h.Partial = h.NumberFound && amountCents > 0 && amountCents < inv.OpenCents
		if h.NumberFound || h.AmountEqual {
			out = append(out, h)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score() > out[j].score() })
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

// numberRe finds digit groups joined by "-", "/" or ".", as invoice numbers
// are written in transfer texts: "RE 2026-0001", "2026/0001", "20260001".
// A space does not join groups here: "2026-0001 vom 15.08.2026" must stay two
// numbers. Two groups separated by exactly one space are tried as one number
// as well ("2026 0001"), in addition to each on its own.
var numberRe = regexp.MustCompile(`\d+(?:[-/.]\d+)*`)

func numbersIn(s string) map[string]bool {
	out := map[string]bool{}
	locs := numberRe.FindAllStringIndex(s, -1)
	for i, loc := range locs {
		out[digits(s[loc[0]:loc[1]])] = true
		if i+1 < len(locs) && locs[i+1][0] == loc[1]+1 && s[loc[1]] == ' ' {
			out[digits(s[loc[0]:locs[i+1][1]])] = true
		}
	}
	return out
}

func digits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

// legalForms are dropped before comparing names: "Muster GmbH" and
// "MUSTER GESMBH WIEN" are the same customer.
var legalForms = map[string]bool{
	"gmbh": true, "gesmbh": true, "mbh": true, "ges": true, "kg": true, "og": true, "ag": true,
	"eu": true, "e": true, "u": true, "co": true, "kgaa": true, "se": true, "ltd": true, "inc": true,
	"und": true, "&": true, "verein": true,
}

// NameTokens normalises a name for comparison: lower case, umlauts folded,
// punctuation and legal forms removed.
func NameTokens(s string) []string {
	s = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss",
		"Ä", "ae", "Ö", "oe", "Ü", "ue").Replace(strings.ToLower(s))
	fields := strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var out []string
	for _, f := range fields {
		if !legalForms[f] && len(f) > 1 {
			out = append(out, f)
		}
	}
	return out
}

// SimilarName reports whether two names plausibly mean the same party: all
// words of the shorter name appear in the longer one, allowing one typo per
// longer word. Banks shorten and add ("MUSTER GMBH WIEN" for "Muster GmbH"),
// so containment fits better than a symmetric distance.
func SimilarName(a, b string) bool {
	ta, tb := NameTokens(a), NameTokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return false
	}
	if len(ta) > len(tb) {
		ta, tb = tb, ta
	}
	for _, x := range ta {
		found := false
		for _, y := range tb {
			if x == y || (len(x) >= 5 && levenshtein(x, y) <= 1) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// noise are words on receipts and bank lines that name no party.
var noise = map[string]bool{
	"pos": true, "dankt": true, "danke": true, "rechnung": true, "beleg": true, "kassa": true,
	"filiale": true, "kartenzahlung": true, "bankomat": true, "lastschrift": true, "einzug": true,
	"zahlung": true, "gutschrift": true, "ueberweisung": true, "sepa": true,
	// Branch towns: card payments name the shop's town.
	"austria": true, "oesterreich": true, "wien": true, "graz": true, "linz": true, "salzburg": true,
	"innsbruck": true, "klagenfurt": true, "villach": true, "wels": true, "poelten": true, "dornbirn": true,
	"steyr": true, "leoben": true, "krems": true, "bregenz": true, "eisenstadt": true, "baden": true,
}

func partyTokens(s string) []string {
	var out []string
	for _, t := range NameTokens(s) {
		digits := 0
		for _, r := range t {
			if unicode.IsDigit(r) {
				digits++
			}
		}
		// Two digits or more is a number, a date or a terminal; "a1" is a name.
		if noise[t] || digits >= 2 {
			continue
		}
		out = append(out, t)
	}
	return out
}

// SimilarParty reports whether a bank line, receipt vendor or booking text
// plausibly names the same party as an earlier one (ADR 0044). It is looser
// than SimilarName: numbers (card numbers, dates, terminals), payment words
// and branch towns are left out, and two thirds of the shorter name's words
// must appear in the longer one, so "BILLA DANKT 1234 WIEN" matches "Billa
// Graz". A one- or two-word name must match in full, so "Magenta Telekom"
// is not "A1 Telekom".
func SimilarParty(a, b string) bool {
	ta, tb := partyTokens(a), partyTokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return false
	}
	if len(ta) > len(tb) {
		ta, tb = tb, ta
	}
	found := 0
	for _, x := range ta {
		for _, y := range tb {
			if x == y || (len(x) >= 5 && levenshtein(x, y) <= 1) {
				found++
				break
			}
		}
	}
	return found*3 >= len(ta)*2 && (len(ta) > 2 || found == len(ta))
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
