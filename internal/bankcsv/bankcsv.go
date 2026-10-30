// Package bankcsv reads a bank's CSV export into transactions.
//
// There is no per-bank parsing here: the caller supplies a Mapping saying which
// column holds what, which is what makes this work with the user's actual bank
// rather than one we guessed at (ADR 0009). What the package does handle is the
// mechanical variation between Austrian exports — delimiter, encoding, amount
// and date notation.
package bankcsv

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chrthal/accountant/internal/money"
)

// MaxFileSize is the largest upload the importer accepts. A year of one
// account's transactions is far smaller; the limit exists because the file is
// carried through the mapping form rather than stored server-side (ADR 0009).
const MaxFileSize = 2 << 20 // 2 MiB

// Mapping says which column index holds which field. Date and Amount are
// required; the others are optional and may be -1.
type Mapping struct {
	Date         int
	Amount       int
	Counterparty int
	Purpose      int
	HasHeader    bool
}

// Row is one parsed transaction.
type Row struct {
	BookedOn     string // 'YYYY-MM-DD'
	AmountCents  int64  // signed: negative is money leaving the account
	Counterparty string
	// CounterpartyIBAN and RawText are only set when the counterparty came
	// out of a labelled text field (SplitText). RawText then keeps the text
	// as the bank sent it.
	CounterpartyIBAN string
	Purpose          string
	RawText          string
	Hash             string // dedup key over the raw record
}

// File is a CSV that has been read and split into records, before mapping.
type File struct {
	Delimiter rune
	Records   [][]string
}

// Parse reads raw upload bytes into records: it decodes the encoding, picks the
// delimiter, and splits the rows. Mapping happens afterwards, once the user has
// seen the columns.
func Parse(raw []byte) (File, error) {
	if len(raw) == 0 {
		return File{}, fmt.Errorf("the file is empty")
	}
	if len(raw) > MaxFileSize {
		return File{}, fmt.Errorf("the file is larger than %d MiB", MaxFileSize>>20)
	}

	text := decode(raw)
	delim := sniffDelimiter(text)

	r := csv.NewReader(strings.NewReader(text))
	r.Comma = delim
	// Bank exports are not always rectangular — trailing empty columns, or a
	// summary line with fewer fields. Reading them anyway beats refusing the
	// whole file over one ragged row.
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	records, err := r.ReadAll()
	if err != nil {
		return File{}, fmt.Errorf("could not read the CSV: %w", err)
	}

	// Drop rows that are entirely empty.
	var kept [][]string
	for _, rec := range records {
		if slicesAllEmpty(rec) {
			continue
		}
		kept = append(kept, rec)
	}
	if len(kept) == 0 {
		return File{}, fmt.Errorf("the file contains no rows")
	}
	return File{Delimiter: delim, Records: kept}, nil
}

// Columns returns the header labels if the file has a header, or generated
// names ("Spalte 1", …) if it does not — so the mapping UI always has something
// to show.
func (f File) Columns(hasHeader bool) []string {
	width := 0
	for _, rec := range f.Records {
		width = max(width, len(rec))
	}
	out := make([]string, width)
	for i := range out {
		if hasHeader && i < len(f.Records[0]) && strings.TrimSpace(f.Records[0][i]) != "" {
			out[i] = strings.TrimSpace(f.Records[0][i])
			continue
		}
		out[i] = fmt.Sprintf("Spalte %d", i+1)
	}
	return out
}

// MapRows turns records into transactions using the mapping. Rows that cannot
// be read as a transaction are reported by index rather than dropped silently:
// a bank export usually ends with a balance line, and the user should see that
// it was skipped rather than wonder where a transaction went.
func (f File) MapRows(m Mapping) (rows []Row, skipped []string, err error) {
	if m.Date < 0 || m.Amount < 0 {
		return nil, nil, fmt.Errorf("date and amount columns must both be mapped")
	}

	records := f.Records
	if m.HasHeader {
		records = records[1:]
	}

	for i, rec := range records {
		lineNo := i + 1
		if m.HasHeader {
			lineNo++
		}

		date, dateErr := field(rec, m.Date)
		amount, amountErr := field(rec, m.Amount)
		if dateErr != nil || amountErr != nil {
			skipped = append(skipped, fmt.Sprintf("Zeile %d: Datums- oder Betragsspalte fehlt", lineNo))
			continue
		}

		booked, err := parseDate(date)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("Zeile %d: %v", lineNo, err))
			continue
		}
		cents, err := parseSignedAmount(amount)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("Zeile %d: %v", lineNo, err))
			continue
		}

		counterparty, _ := field(rec, m.Counterparty)
		purpose, _ := field(rec, m.Purpose)

		row := Row{
			BookedOn:     booked,
			AmountCents:  cents,
			Counterparty: strings.TrimSpace(counterparty),
			Purpose:      strings.TrimSpace(purpose),
			Hash:         hashRow(rec, booked, cents),
		}
		if row.Counterparty == "" {
			if l, ok := SplitText(row.Purpose); ok {
				row.RawText = row.Purpose
				row.Counterparty, row.CounterpartyIBAN, row.Purpose = l.Counterparty, l.IBAN, l.Purpose
			}
		}
		rows = append(rows, row)
	}

	if len(rows) == 0 {
		return nil, skipped, fmt.Errorf("no usable transactions found — check the column mapping")
	}
	return rows, skipped, nil
}

// hashRow is the dedup key: the raw record plus the values actually read from
// it. Re-importing the same export, or an overlapping date range, must not
// double a transaction (ADR 0009).
func hashRow(rec []string, bookedOn string, cents int64) string {
	h := sha256.New()
	for _, f := range rec {
		h.Write([]byte(strings.TrimSpace(f)))
		h.Write([]byte{0})
	}
	fmt.Fprintf(h, "|%s|%d", bookedOn, cents)
	return hex.EncodeToString(h.Sum(nil))
}

func field(rec []string, idx int) (string, error) {
	if idx < 0 || idx >= len(rec) {
		return "", fmt.Errorf("column %d is not present", idx+1)
	}
	return rec[idx], nil
}

// decode converts the upload to UTF-8. Bank exports are still often cp1252, and
// the tell is simply that the bytes are not valid UTF-8.
func decode(raw []byte) string {
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM
	if utf8.Valid(raw) {
		return string(raw)
	}
	// cp1252 maps 0x80–0x9F to specific characters; the rest is latin-1. Only
	// the characters that turn up in Austrian bank data are worth mapping.
	var b strings.Builder
	for _, c := range raw {
		switch {
		case c < 0x80:
			b.WriteByte(c)
		case c == 0x80:
			b.WriteRune('€')
		case c == 0x9F:
			b.WriteRune('Ÿ')
		default:
			b.WriteRune(rune(c)) // latin-1 maps 1:1 onto the first Unicode block
		}
	}
	return b.String()
}

// sniffDelimiter picks between ';' and ',' by which one appears more often on
// the first line. German-locale exports use ';' precisely because the comma is
// the decimal separator.
func sniffDelimiter(text string) rune {
	line, _, _ := strings.Cut(text, "\n")
	if strings.Count(line, ";") >= strings.Count(line, ",") {
		return ';'
	}
	return ','
}

// parseDate accepts the two notations that actually appear: DD.MM.YYYY and
// YYYY-MM-DD. Anything else is reported rather than guessed at — misreading a
// booking date is worse than refusing the row.
func parseDate(s string) (string, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02", "02.01.2006", "02.01.06"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02"), nil
		}
	}
	return "", fmt.Errorf("%q ist kein Datum (erwartet TT.MM.JJJJ oder JJJJ-MM-TT)", s)
}

// parseSignedAmount reads an amount that may carry a leading or trailing sign.
func parseSignedAmount(s string) (int64, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "EUR")
	s = strings.TrimSpace(s)

	negative := false
	switch {
	case strings.HasPrefix(s, "-"):
		negative, s = true, strings.TrimSpace(s[1:])
	case strings.HasPrefix(s, "+"):
		s = strings.TrimSpace(s[1:])
	case strings.HasSuffix(s, "-"): // some exports put the sign last
		negative, s = true, strings.TrimSpace(s[:len(s)-1])
	}

	cents, err := money.Parse(s)
	if err != nil {
		return 0, fmt.Errorf("%q ist kein Betrag", s)
	}
	if cents == 0 {
		return 0, fmt.Errorf("Betrag ist 0")
	}
	if negative {
		return -cents, nil
	}
	return cents, nil
}

func slicesAllEmpty(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}
