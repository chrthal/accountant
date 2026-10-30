package bankcsv

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Guess proposes a mapping by looking at the values rather than at column
// names, because a header row is exactly what some exports lack (the meinELBA
// export has none, and an assumed header swallowed its first transaction).
//
// A date column is one whose values read as dates, and an amount column one
// whose values read as signed amounts with a decimal separator. The file has a
// header when its first row does not read as a date in that column.
// Counterparty and purpose are guessed only from header names, or as the
// longest text column for the purpose. The user confirms all of it on the
// mapping page, and a guess is never imported unseen.
func (f File) Guess() Mapping {
	m := Mapping{Date: -1, Amount: -1, Counterparty: -1, Purpose: -1}
	if len(f.Records) == 0 {
		return m
	}

	// Judge columns on the rows after the first (the first may be a header),
	// unless the first is all there is.
	sample := f.Records
	if len(sample) > 1 {
		sample = sample[1:]
	}
	if len(sample) > 30 {
		sample = sample[:30]
	}
	width := 0
	for _, rec := range f.Records {
		width = max(width, len(rec))
	}

	score := func(col int, ok func(string) bool) float64 {
		hits, seen := 0, 0
		for _, rec := range sample {
			v, err := field(rec, col)
			if err != nil || strings.TrimSpace(v) == "" {
				continue
			}
			seen++
			if ok(v) {
				hits++
			}
		}
		if seen == 0 {
			return 0
		}
		return float64(hits) / float64(seen)
	}
	isDate := func(v string) bool { _, err := parseDate(v); return err == nil }
	isAmount := func(v string) bool {
		if isDate(v) || !strings.ContainsAny(v, ",.") {
			return false
		}
		_, err := parseSignedAmount(v)
		return err == nil
	}

	// The first date column wins: exports list the booking date before the
	// value date (Valuta), and the booking date is the one the ledger uses.
	for col := range width {
		if score(col, isDate) >= 0.8 {
			m.Date = col
			break
		}
	}
	best := 0.0
	for col := range width {
		if col == m.Date {
			continue
		}
		if sc := score(col, isAmount); sc >= 0.8 && sc > best {
			m.Amount, best = col, sc
		}
	}

	first := f.Records[0]
	if m.Date >= 0 && len(f.Records) > 1 {
		v, err := field(first, m.Date)
		m.HasHeader = err != nil || !isDate(v)
	}

	// Name-based hints, only when there is a header to read names from.
	if m.HasHeader {
		for col, name := range first {
			n := strings.ToLower(name)
			switch {
			case m.Counterparty < 0 && containsAny(n, "empfänger", "auftraggeber", "partner", "name", "gegenkonto"):
				m.Counterparty = col
			case m.Purpose < 0 && containsAny(n, "zweck", "text", "buchungstext", "verwendung", "beschreibung"):
				m.Purpose = col
			}
		}
	}
	// Otherwise the longest text column is almost always the purpose.
	if m.Purpose < 0 {
		longest := 0
		for col := range width {
			if col == m.Date || col == m.Amount || col == m.Counterparty {
				continue
			}
			if score(col, isDate) > 0.5 || score(col, isAmount) > 0.5 {
				continue
			}
			total := 0
			for _, rec := range sample {
				if v, err := field(rec, col); err == nil {
					total += len(strings.TrimSpace(v))
				}
			}
			if total > longest {
				m.Purpose, longest = col, total
			}
		}
	}
	return m
}

// Signature identifies a file's column layout, so a mapping confirmed once
// can be offered again for the next export of the same account: it is the
// delimiter, the column count, and the header row if there is one. Two
// headerless exports with the same column count share a signature. That is
// deliberate, since without a header nothing else distinguishes them, and the
// user still sees and confirms the mapping.
func (f File) Signature(hasHeader bool) string {
	width := 0
	for _, rec := range f.Records {
		width = max(width, len(rec))
	}
	h := sha256.New()
	h.Write([]byte{byte(f.Delimiter), byte(width)})
	if hasHeader && len(f.Records) > 0 {
		for _, c := range f.Records[0] {
			h.Write([]byte(strings.ToLower(strings.TrimSpace(c))))
			h.Write([]byte{0})
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
