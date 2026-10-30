package money

import "testing"

func TestFormatLangRoundTrips(t *testing.T) {
	for _, c := range []int64{0, 5, 123456, -123456789, 100000000} {
		for _, lang := range []string{"de", "en"} {
			s := FormatLang(c, lang)
			if c < 0 {
				s = s[1:]
			}
			back, err := Parse(s)
			want := c
			if want < 0 {
				want = -want
			}
			if err != nil || back != want {
				t.Errorf("FormatLang(%d, %s) = %q, parses back to %d (%v)", c, lang, s, back, err)
			}
		}
	}
	if got := FormatLang(123456, "en"); got != "1,234.56" {
		t.Errorf("en = %q", got)
	}
	if got := FormatLang(123456, "de"); got != "1.234,56" {
		t.Errorf("de = %q", got)
	}
}
