package bankcsv_test

import (
	"testing"

	"github.com/chrthal/accountant/internal/bankcsv"
)

func TestSplitLabelledText(t *testing.T) {
	for in, want := range map[string]bankcsv.Labelled{
		"Empfänger: Hosting AG IBAN Empfänger: AT00 1234 5678 Verwendungszweck: Server August": {
			Counterparty: "Hosting AG", IBAN: "AT0012345678", Purpose: "Server August"},
		"Auftraggeber: Muster GmbH IBAN Auftraggeber: AT009999 BIC Auftraggeber: RZOOAT2L Zahlungsreferenz: RE 2026-0001": {
			Counterparty: "Muster GmbH", IBAN: "AT009999", Purpose: "RE 2026-0001"},
		"Empfänger:   SVS  Verwendungszweck: Beitrag Q3 Zahlungsreferenz: 4711 Mandat: M-1": {
			Counterparty: "SVS", Purpose: "Beitrag Q3 4711"},
	} {
		got, ok := bankcsv.SplitText(in)
		if !ok || got != want {
			t.Errorf("SplitText(%q) = %+v, %v; want %+v", in, got, ok, want)
		}
	}
	for _, in := range []string{"Kartenzahlung Billa 1010 Wien", "", "Verwendungszweck: nur Zweck"} {
		if _, ok := bankcsv.SplitText(in); ok {
			t.Errorf("SplitText(%q) split text without a counterparty label", in)
		}
	}
}

// The headerless meinELBA shape from guess_test.go, mapped the way the guess
// maps it: the counterparty comes out of the text field, and the original
// text is kept.
func TestMapRowsSplitsTheTextField(t *testing.T) {
	f, _ := bankcsv.Parse([]byte(elbaLikeCSV))
	rows, _, err := f.MapRows(f.Guess())
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Counterparty != "Hosting AG" || rows[0].CounterpartyIBAN != "AT001234" || rows[0].Purpose != "Server August" {
		t.Errorf("row 1 = %+v", rows[0])
	}
	if rows[0].RawText == "" {
		t.Error("the bank's original text was not kept")
	}
	if rows[2].Counterparty != "" || rows[2].Purpose != "Kartenzahlung Billa 1010 Wien" || rows[2].RawText != "" {
		t.Errorf("unlabelled card payment was altered: %+v", rows[2])
	}
	// The dedup hash is over the raw record, so it must not change.
	again, _, _ := f.MapRows(f.Guess())
	if again[0].Hash != rows[0].Hash {
		t.Error("hash is not stable")
	}
}
