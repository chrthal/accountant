package money

import "strings"

// FormatLang formats cents for a UI language: "1.234,56" in German,
// "1,234.56" in English (ADR 0024). Parse reads both, so a value written in
// either format comes back the same.
func FormatLang(cents int64, lang string) string {
	s := Format(cents)
	if lang != "en" {
		return s
	}
	return strings.NewReplacer(".", ",", ",", ".").Replace(s)
}
