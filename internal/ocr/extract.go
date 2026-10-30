package ocr

import (
	"regexp"
	"strings"
	"time"

	"github.com/chrthal/accountant/internal/money"
)

// Draft is what was read off a receipt. Every field is optional: a field that
// could not be read stays empty rather than guessed at, because an empty amount
// is a visible gap and a wrong amount is not (ADR 0011).
type Draft struct {
	AmountCents int64  // 0 when no total could be identified
	BookedOn    string // 'YYYY-MM-DD', empty when no date was found
	Vendor      string
	VATRate     int // 0 when no rate could be read; 0 is also a legitimate rate
	VATRateRead bool
	// InvoiceNumber is the supplier's own number, when the Beleg is an
	// invoice that prints one. It goes into the booking text, so the booking
	// can be traced back to the paper.
	InvoiceNumber string
	RawText       string
}

// Description is the suggested booking text: the vendor, plus the supplier's
// invoice number when there is one.
func (d Draft) Description() string {
	switch {
	case d.Vendor != "" && d.InvoiceNumber != "":
		return d.Vendor + " – Rechnung " + d.InvoiceNumber
	case d.InvoiceNumber != "":
		return "Rechnung " + d.InvoiceNumber
	}
	return d.Vendor
}

// Found reports whether anything usable came out, so the UI can say "nothing
// recognised" instead of showing an empty form with no explanation.
func (d Draft) Found() bool {
	return d.AmountCents > 0 || d.BookedOn != "" || d.Vendor != ""
}

// totalKeywords are the words Austrian receipts and invoices put next to the
// amount due. Order matters: the more specific a keyword, the more it is
// trusted.
var totalKeywords = []string{
	"zu zahlen", "rechnungsbetrag", "gesamtbetrag", "gesamtsumme", "endbetrag",
	"zahlbetrag", "summe", "gesamt", "total", "brutto",
}

// netOrTaxLine reports whether a line states a subtotal rather than the amount
// due: invoices print "Summe netto" and "USt 20 %" above the gross total, and
// both contain a total keyword or an amount.
func netOrTaxLine(lower string) bool {
	if strings.Contains(lower, "brutto") || strings.Contains(lower, "zu zahlen") {
		return false
	}
	for _, w := range []string{"netto", "ust", "mwst", "umsatzsteuer", "zwischensumme"} {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

var (
	// Labels a supplier's invoice number sits behind.
	invoiceNoRe = regexp.MustCompile(`(?i)\b(?:rechnungs?[- ]?(?:nr|nummer)|re[- ]?nr|invoice\s*(?:no|number))\.?\s*:?\s*([A-Z0-9][A-Z0-9/\-_.]{1,30})`)
	// A title line "Rechnung 2026-0001" with no "Nr." label, which is how this
	// app's own PDF writer prints it. The number must contain a digit, so a
	// plain "RECHNUNG" heading does not count.
	invoiceTitleRe = regexp.MustCompile(`(?im)^\s*rechnung\s+([A-Z0-9]*\d[A-Z0-9/\-_.]*)\s*$`)
	// Labels that name the invoice's own date, as opposed to delivery or due dates.
	dateLabelRe = regexp.MustCompile(`(?i)rechnungsdatum|belegdatum|ausstellungsdatum|^\s*datum\b|\bdatum\s*:`)
	// pdftotext -layout puts columns side by side; three or more spaces are
	// a column gap, not a word gap.
	columnGapRe = regexp.MustCompile(`\s{3,}`)
	// Address lines: a street with a house number, or a postcode and town.
	// Neither is a business name.
	addressRe = regexp.MustCompile(`(?i)(gasse|straße|strasse|str\.|weg|platz|allee|ring|zeile|kai)\s*\d|\b\d{4,5}\s+\p{Lu}`)
)

var (
	// 1.234,56 / 1234,56 / 1234.56 — at least one decimal separator, so a
	// receipt number or a date fragment is not mistaken for money.
	amountRe = regexp.MustCompile(`\d{1,3}(?:[.\s]\d{3})*[,.]\d{2}\b|\d+[,.]\d{2}\b`)
	dateRe   = regexp.MustCompile(`\b(\d{1,2})[.\-/](\d{1,2})[.\-/](\d{2,4})\b|\b(\d{4})-(\d{2})-(\d{2})\b`)
	vatRe    = regexp.MustCompile(`(?i)\b(20|13|10)\s*(?:,\d+)?\s*%`)
)

// Extract reads a draft out of recognised text.
//
// The rules are deliberately small: find the amount next to a total keyword,
// the first plausible date, the first line that looks like a business name, and
// a VAT rate if one is printed. Anything more elaborate would be guesswork
// dressed up as accuracy.
//
// exclude lists names that must not be taken as the vendor: on an incoming
// invoice the user's own business is printed as the recipient.
func Extract(text string, exclude ...string) Draft {
	d := Draft{RawText: text}
	lines := strings.Split(text, "\n")

	d.AmountCents = findTotal(lines)
	d.BookedOn = findLabelledDate(lines)
	if d.BookedOn == "" {
		d.BookedOn = findDate(text)
	}
	d.Vendor = findVendor(splitColumns(lines), exclude)
	if rate, ok := findVATRate(text); ok {
		d.VATRate, d.VATRateRead = rate, true
	}
	if m := invoiceNoRe.FindStringSubmatch(text); m != nil {
		d.InvoiceNumber = strings.TrimRight(m[1], ".-_/")
	} else if m := invoiceTitleRe.FindStringSubmatch(text); m != nil {
		d.InvoiceNumber = strings.TrimRight(m[1], ".-_/")
	}
	return d
}

// splitColumns breaks layout-preserved lines at column gaps, so a letterhead
// printed beside "RECHNUNG" is still its own line.
func splitColumns(lines []string) []string {
	var out []string
	for _, l := range lines {
		for _, part := range columnGapRe.Split(l, -1) {
			if strings.TrimSpace(part) != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// findLabelledDate prefers a date printed next to "Rechnungsdatum" or
// "Datum". An invoice also carries delivery, service and due dates, and the
// first date on the page is not reliably the invoice's own.
func findLabelledDate(lines []string) string {
	for _, l := range lines {
		if dateLabelRe.MatchString(l) {
			// Only the part after the label: a layout line may hold a
			// different date in another column before it.
			loc := dateLabelRe.FindStringIndex(l)
			if d := findDate(l[loc[0]:]); d != "" {
				return d
			}
		}
	}
	return ""
}

// findTotal prefers an amount on a line carrying a total keyword, taking the
// last one on that line — receipts print "SUMME EUR 12,34", not the reverse.
// With no keyword anywhere, the largest amount on the receipt is the best
// available guess at the total.
func findTotal(lines []string) int64 {
	for _, keyword := range totalKeywords {
		for _, line := range lines {
			lower := strings.ToLower(line)
			if !strings.Contains(lower, keyword) || netOrTaxLine(lower) {
				continue
			}
			matches := amountRe.FindAllString(line, -1)
			if len(matches) == 0 {
				continue
			}
			if cents, err := money.Parse(normaliseAmount(matches[len(matches)-1])); err == nil && cents > 0 {
				return cents
			}
		}
	}

	var best int64
	for _, line := range lines {
		for _, m := range amountRe.FindAllString(line, -1) {
			if cents, err := money.Parse(normaliseAmount(m)); err == nil && cents > best {
				best = cents
			}
		}
	}
	return best
}

// normaliseAmount removes the spaces OCR sometimes leaves in thousands groups.
func normaliseAmount(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), " ", "")
}

func findDate(text string) string {
	for _, m := range dateRe.FindAllStringSubmatch(text, -1) {
		var layouts []string
		var candidate string

		if m[4] != "" { // ISO form
			candidate = m[4] + "-" + m[5] + "-" + m[6]
			layouts = []string{"2006-01-02"}
		} else {
			candidate = m[1] + "." + m[2] + "." + m[3]
			layouts = []string{"2.1.2006", "2.1.06"}
		}

		for _, l := range layouts {
			t, err := time.Parse(l, candidate)
			if err != nil {
				continue
			}
			// A receipt from 1970 or 2099 is a misread, not a date.
			if t.Year() < 2000 || t.Year() > time.Now().Year()+1 {
				continue
			}
			return t.Format("2006-01-02")
		}
	}
	return ""
}

// findVendor takes the first line that reads like a business name: letters, not
// a date or an amount, and not one of the header words receipts start with.
func findVendor(lines []string, exclude []string) string {
	skip := []string{"rechnung", "kassenbon", "beleg", "quittung", "kassabon", "receipt", "invoice",
		// Header and contact lines. Written with their punctuation, so that
		// "tel." does not also skip "Hotel Sacher".
		"seite ", "page ", "datum", "kundennummer", "kunden-nr", "uid-", "uid:", "uid ",
		"tel.", "tel:", "telefon", "fax", "e-mail", "www.", "iban", "bic"}
	for _, e := range exclude {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			skip = append(skip, e)
		}
	}

	for _, line := range lines {
		l := strings.TrimSpace(line)
		if len([]rune(l)) < 3 || len([]rune(l)) > 60 {
			continue
		}
		if amountRe.MatchString(l) || dateRe.MatchString(l) || addressRe.MatchString(l) {
			continue
		}

		letters := 0
		for _, r := range l {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || strings.ContainsRune("äöüÄÖÜß", r) {
				letters++
			}
		}
		if letters < 3 {
			continue
		}

		lower := strings.ToLower(l)
		var skipped bool
		for _, s := range skip {
			if strings.Contains(lower, s) {
				skipped = true
				break
			}
		}
		if skipped {
			continue
		}
		return l
	}
	return ""
}

// findVATRate accepts only rates the app knows (20, 13, 10). A receipt printing
// "7 %" is German, not Austrian, and is better left for the user to enter.
func findVATRate(text string) (int, bool) {
	m := vatRe.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	switch m[1] {
	case "20":
		return 20, true
	case "13":
		return 13, true
	case "10":
		return 10, true
	}
	return 0, false
}
