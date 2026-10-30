package bankcsv_test

import (
	"testing"

	"github.com/chrthal/accountant/internal/bankcsv"
)

// Shaped like a meinELBA (Raiffeisen) export: no header row, booking date,
// one combined text field, value date, amount, currency, timestamp. The
// values are made up. The real export is not committed (plan 0001).
const elbaLikeCSV = "\xef\xbb\xbf" + `03.08.2026;Empfänger: Hosting AG IBAN Empfänger: AT001234 Verwendungszweck: Server August;03.08.2026;-540,00;EUR;03.08.2026 10:11:12:123
05.08.2026;Auftraggeber: Muster GmbH IBAN Auftraggeber: AT009999 Verwendungszweck: Honorar Juli;05.08.2026;200,68;EUR;05.08.2026 08:00:01:000
12.08.2026;Kartenzahlung Billa 1010 Wien;11.08.2026;-59,94;EUR;12.08.2026 06:12:00:500
`

func TestGuessHeaderlessExport(t *testing.T) {
	f, err := bankcsv.Parse([]byte(elbaLikeCSV))
	if err != nil {
		t.Fatal(err)
	}
	m := f.Guess()
	if m.HasHeader {
		t.Error("a file whose first row is a transaction was guessed to have a header")
	}
	if m.Date != 0 || m.Amount != 3 || m.Purpose != 1 || m.Counterparty != -1 {
		t.Errorf("guess = %+v, want date 0, amount 3, purpose 1, no counterparty", m)
	}
	rows, skipped, err := f.MapRows(m)
	if err != nil || len(rows) != 3 || len(skipped) != 0 {
		t.Fatalf("guessed mapping imports %d rows (skipped %v, err %v), want all 3", len(rows), skipped, err)
	}
	if rows[0].AmountCents != -54000 {
		t.Errorf("first row = %+v — the first transaction must not be lost as a header", rows[0])
	}
}

func TestGuessExportWithHeader(t *testing.T) {
	f, _ := bankcsv.Parse([]byte(germanCSV))
	m := f.Guess()
	want := bankcsv.Mapping{Date: 0, Amount: 1, Counterparty: 2, Purpose: 3, HasHeader: true}
	if m != want {
		t.Errorf("guess = %+v, want %+v", m, want)
	}
}

func TestSignatureTracksTheLayout(t *testing.T) {
	a, _ := bankcsv.Parse([]byte(germanCSV))
	b, _ := bankcsv.Parse([]byte("Buchungsdatum;Betrag;Empfänger;Verwendungszweck\n01.01.2026;1,00;X;Y\n"))
	c, _ := bankcsv.Parse([]byte(elbaLikeCSV))
	if a.Signature(true) != b.Signature(true) {
		t.Error("two exports of the same layout have different signatures")
	}
	if a.Signature(true) == c.Signature(false) {
		t.Error("different layouts share a signature")
	}
}
