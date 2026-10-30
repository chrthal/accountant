package bankcsv_test

import (
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/bankcsv"
)

// A German-locale export: semicolon-separated, comma decimals, dotted dates.
const germanCSV = `Buchungsdatum;Betrag;Empfänger;Verwendungszweck
15.03.2026;-1.234,56;Büro Müller GmbH;Rechnung 2026-0042
16.03.2026;2.000,00;Muster GmbH;Honorar März
17.03.2026;-49,90;Hosting AG;Server März
`

func mapping() bankcsv.Mapping {
	return bankcsv.Mapping{Date: 0, Amount: 1, Counterparty: 2, Purpose: 3, HasHeader: true}
}

func TestParseGermanExport(t *testing.T) {
	f, err := bankcsv.Parse([]byte(germanCSV))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.Delimiter != ';' {
		t.Errorf("delimiter = %q, want ';'", f.Delimiter)
	}

	cols := f.Columns(true)
	if len(cols) != 4 || cols[0] != "Buchungsdatum" || cols[3] != "Verwendungszweck" {
		t.Errorf("columns = %v, want the header labels", cols)
	}

	rows, skipped, err := f.MapRows(mapping())
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %v, want none", skipped)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}

	// Signs matter: money leaving the account must stay negative.
	if rows[0].AmountCents != -123456 {
		t.Errorf("row 1 amount = %d, want -123456", rows[0].AmountCents)
	}
	if rows[1].AmountCents != 200000 {
		t.Errorf("row 2 amount = %d, want 200000", rows[1].AmountCents)
	}
	if rows[0].BookedOn != "2026-03-15" {
		t.Errorf("row 1 date = %q, want 2026-03-15", rows[0].BookedOn)
	}
	if rows[0].Counterparty != "Büro Müller GmbH" || rows[0].Purpose != "Rechnung 2026-0042" {
		t.Errorf("row 1 text fields = %q / %q", rows[0].Counterparty, rows[0].Purpose)
	}
}

// The same data as a comma-separated, ISO-dated, dot-decimal export.
func TestParseInternationalExport(t *testing.T) {
	const csv = `date,amount,name,reference
2026-03-15,-1234.56,Büro Müller GmbH,Rechnung 2026-0042
2026-03-16,2000.00,Muster GmbH,Honorar März
`
	f, err := bankcsv.Parse([]byte(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.Delimiter != ',' {
		t.Errorf("delimiter = %q, want ','", f.Delimiter)
	}
	rows, _, err := f.MapRows(mapping())
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if len(rows) != 2 || rows[0].AmountCents != -123456 || rows[1].AmountCents != 200000 {
		t.Errorf("rows = %+v", rows)
	}
}

// cp1252 is still common in bank exports; umlauts must survive it.
func TestParseCP1252Encoding(t *testing.T) {
	utf8Text := "Datum;Betrag;Empfänger\n15.03.2026;-10,00;Büro Müller\n"
	var cp1252 []byte
	for _, r := range utf8Text {
		if r < 0x100 {
			cp1252 = append(cp1252, byte(r))
		}
	}

	f, err := bankcsv.Parse(cp1252)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rows, _, err := f.MapRows(bankcsv.Mapping{Date: 0, Amount: 1, Counterparty: 2, Purpose: -1, HasHeader: true})
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if rows[0].Counterparty != "Büro Müller" {
		t.Errorf("counterparty = %q, want the umlauts decoded", rows[0].Counterparty)
	}
}

func TestSignVariants(t *testing.T) {
	const csv = `Datum;Betrag
15.03.2026;1.234,56-
16.03.2026;+500,00
17.03.2026;-49,90 EUR
`
	f, _ := bankcsv.Parse([]byte(csv))
	rows, skipped, err := f.MapRows(bankcsv.Mapping{Date: 0, Amount: 1, Counterparty: -1, Purpose: -1, HasHeader: true})
	if err != nil {
		t.Fatalf("map: %v (skipped %v)", err, skipped)
	}
	want := []int64{-123456, 50000, -4990}
	for i, w := range want {
		if rows[i].AmountCents != w {
			t.Errorf("row %d = %d, want %d", i+1, rows[i].AmountCents, w)
		}
	}
}

// Bank exports routinely end with a balance or summary line. It must be
// reported as skipped, not silently dropped and not fatal.
func TestUnusableRowsAreReportedNotDropped(t *testing.T) {
	const csv = `Datum;Betrag;Text
15.03.2026;-10,00;Kauf
Kontostand zum 31.03.2026;;
31.03.2026;kein Betrag;Unsinn
`
	f, _ := bankcsv.Parse([]byte(csv))
	rows, skipped, err := f.MapRows(mapping())
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d usable rows, want 1", len(rows))
	}
	if len(skipped) != 2 {
		t.Errorf("skipped = %v, want both unusable rows reported", skipped)
	}
}

// The dedup key must be stable across imports of the same file, and differ
// between transactions that merely look similar.
func TestHashIsStableAndDistinct(t *testing.T) {
	f1, _ := bankcsv.Parse([]byte(germanCSV))
	f2, _ := bankcsv.Parse([]byte(germanCSV))
	a, _, _ := f1.MapRows(mapping())
	b, _, _ := f2.MapRows(mapping())

	for i := range a {
		if a[i].Hash != b[i].Hash {
			t.Errorf("row %d: hash differs between identical imports", i)
		}
	}
	seen := map[string]bool{}
	for _, r := range a {
		if seen[r.Hash] {
			t.Errorf("duplicate hash for distinct transactions: %s", r.Hash)
		}
		seen[r.Hash] = true
	}
}

func TestParseRejectsUnusableFiles(t *testing.T) {
	if _, err := bankcsv.Parse(nil); err == nil {
		t.Error("empty upload accepted")
	}
	if _, err := bankcsv.Parse([]byte(strings.Repeat("x", bankcsv.MaxFileSize+1))); err == nil {
		t.Error("oversized upload accepted")
	}

	f, err := bankcsv.Parse([]byte(germanCSV))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, _, err := f.MapRows(bankcsv.Mapping{Date: -1, Amount: 1}); err == nil {
		t.Error("mapping without a date column accepted")
	}
	// Pointing the mapping at the wrong columns must fail loudly rather than
	// import nonsense.
	if _, _, err := f.MapRows(bankcsv.Mapping{Date: 2, Amount: 3, HasHeader: true}); err == nil {
		t.Error("mapping onto text columns produced transactions")
	}
}
