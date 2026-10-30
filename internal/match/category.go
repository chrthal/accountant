package match

import (
	"strings"
)

// Category is what the category rule needs to know (ADR 0021).
type Category struct {
	ID        int64
	Name      string
	Direction string   // income, outgoing or both
	Keywords  []string // lower-cased, from the category's keyword list
}

// CategoryHit is the category a bank line was assigned to, and why.
type CategoryHit struct {
	ID     int64
	Reason string // e.g. `Stichwort „billa“`
}

// CategoryFor picks the one category whose keywords appear in a bank line's
// text, or, when no keyword fits anywhere, the one whose name does. A line
// that hits two categories gets none: "SPAR … MAGENTA …" is ambiguous however
// the keywords compare, and is better left for the user or the model.
func CategoryFor(text, direction string, cats []Category) (CategoryHit, bool) {
	words := fold(text)
	byKeyword := map[int64]string{}
	byName := map[int64]string{}
	for _, c := range cats {
		if c.Direction != direction && c.Direction != "both" {
			continue
		}
		for _, k := range c.Keywords {
			if containsWord(words, k) {
				byKeyword[c.ID] = "Stichwort „" + k + "“"
				break
			}
		}
		for _, t := range NameTokens(c.Name) {
			if len(t) >= 4 && containsWord(words, t) {
				byName[c.ID] = "Name der Kategorie im Text"
				break
			}
		}
	}
	for _, hits := range []map[int64]string{byKeyword, byName} {
		switch len(hits) {
		case 0:
			continue
		case 1:
			for id, reason := range hits {
				return CategoryHit{ID: id, Reason: reason}, true
			}
		}
		return CategoryHit{}, false // ambiguous
	}
	return CategoryHit{}, false
}

// fold lower-cases and folds umlauts, and turns everything that is not a
// letter or digit into a single space, with a space at both ends, so word
// starts can be found with a plain substring search.
func fold(s string) string {
	s = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss",
		"Ä", "ae", "Ö", "oe", "Ü", "ue").Replace(strings.ToLower(s))
	var b strings.Builder
	b.WriteByte(' ')
	space := true
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r > 127 {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	if !space {
		b.WriteByte(' ')
	}
	return b.String()
}

// containsWord reports whether word starts a word in folded text: "telefon"
// matches "Telefonrechnung". A keyword of three letters or fewer must be a
// whole word, so "a1" does not fire on a customer number like "A12345".
func containsWord(folded, word string) bool {
	word = strings.TrimSpace(fold(word))
	if word == "" {
		return false
	}
	if len(word) <= 3 {
		return strings.Contains(folded, " "+word+" ")
	}
	return strings.Contains(folded, " "+word)
}
