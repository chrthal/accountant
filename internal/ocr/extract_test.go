package ocr_test

import (
	"os"
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/ocr"
)

// Realistic Tesseract output from an Austrian supermarket receipt, including
// the noise it usually contains.
const supermarktBon = `BILLA AG
Filiale 1234
Hauptstrasse 5
1010 Wien

Vollmilch 1L          1,29
Brot                  2,49
Kaffee               11,99

SUMME EUR            15,77
Geg. BAR             20,00
Retour                4,23

MwSt 10% aus         15,77
Datum 15.03.2026 14:32
Vielen Dank fuer Ihren Einkauf
`

func TestExtractSupermarketReceipt(t *testing.T) {
	d := ocr.Extract(supermarktBon)

	if d.AmountCents != 1577 {
		t.Errorf("amount = %d, want 1577 (the SUMME line, not the cash tendered)", d.AmountCents)
	}
	if d.BookedOn != "2026-03-15" {
		t.Errorf("date = %q, want 2026-03-15", d.BookedOn)
	}
	if d.Vendor != "BILLA AG" {
		t.Errorf("vendor = %q, want BILLA AG", d.Vendor)
	}
	if !d.VATRateRead || d.VATRate != 10 {
		t.Errorf("VAT rate = %d (read %v), want 10", d.VATRate, d.VATRateRead)
	}
	if !d.Found() {
		t.Error("Found() is false despite reading everything")
	}
}

// "Geg. BAR 20,00" is larger than the total: a keyword must beat the largest
// amount, or every cash receipt books the wrong figure.
func TestTotalKeywordBeatsLargerAmounts(t *testing.T) {
	d := ocr.Extract("Artikel 5,00\nZU ZAHLEN 7,50\nGegeben BAR 50,00\n")
	if d.AmountCents != 750 {
		t.Errorf("amount = %d, want 750 from the ZU ZAHLEN line", d.AmountCents)
	}
}

func TestTotalKeywordVariants(t *testing.T) {
	cases := map[string]int64{
		"Gesamtbetrag: 42,00":    4200,
		"GESAMT        1.234,56": 123456,
		"Total EUR 9,99":         999,
		"Brutto 120,00":          12000,
		"Zahlbetrag 15,50":       1550,
	}
	for line, want := range cases {
		if got := ocr.Extract(line).AmountCents; got != want {
			t.Errorf("Extract(%q) amount = %d, want %d", line, got, want)
		}
	}
}

// Without any keyword the largest amount is the best available guess.
func TestFallsBackToLargestAmount(t *testing.T) {
	d := ocr.Extract("Kaffee 3,20\nKuchen 4,80\n8,00\n")
	if d.AmountCents != 800 {
		t.Errorf("amount = %d, want 800", d.AmountCents)
	}
}

func TestDateFormats(t *testing.T) {
	cases := map[string]string{
		"Datum 15.03.2026":    "2026-03-15",
		"01.12.25":            "2025-12-01",
		"Beleg 2026-03-15":    "2026-03-15",
		"5.7.2026 10:00":      "2026-07-05",
		"Rechnung 15/03/2026": "2026-03-15",
	}
	for text, want := range cases {
		if got := ocr.Extract(text).BookedOn; got != want {
			t.Errorf("Extract(%q) date = %q, want %q", text, got, want)
		}
	}

	// A misread that yields an implausible year must be refused, not booked.
	for _, text := range []string{"Beleg 15.03.1970", "Karte 12/99", "Nr 31.31.2026"} {
		if got := ocr.Extract(text).BookedOn; got != "" {
			t.Errorf("Extract(%q) date = %q, want it refused", text, got)
		}
	}
}

// A field that could not be read must stay empty rather than be invented.
func TestUnreadableFieldsStayEmpty(t *testing.T) {
	d := ocr.Extract("~~~ ### ???\n\n")
	if d.AmountCents != 0 || d.BookedOn != "" || d.VATRateRead {
		t.Errorf("noise produced a draft: %+v", d)
	}
	if d.Found() {
		t.Error("Found() is true for unrecognisable text")
	}
	if d.RawText == "" {
		t.Error("the raw text must be kept so the user can see what was read")
	}
}

func TestVATRateOnlyAcceptsAustrianRates(t *testing.T) {
	for text, want := range map[string]int{"MwSt 20%": 20, "USt 13 %": 13, "10% MwSt": 10} {
		d := ocr.Extract(text)
		if !d.VATRateRead || d.VATRate != want {
			t.Errorf("Extract(%q) rate = %d (read %v), want %d", text, d.VATRate, d.VATRateRead, want)
		}
	}
	// 7 % is a German rate; leaving it to the user beats booking a wrong one.
	for _, text := range []string{"MwSt 7%", "19 % MwSt", "kein Satz"} {
		if d := ocr.Extract(text); d.VATRateRead {
			t.Errorf("Extract(%q) accepted rate %d", text, d.VATRate)
		}
	}
}

func TestVendorSkipsHeadersAndNumbers(t *testing.T) {
	d := ocr.Extract("KASSENBON\n15.03.2026\n12,34\nCafé Central GmbH\n")
	if d.Vendor != "Café Central GmbH" {
		t.Errorf("vendor = %q, want the business name", d.Vendor)
	}
}

// OCR of thermal paper routinely leaves spaces inside thousands groups.
func TestHandlesSpacedThousands(t *testing.T) {
	if got := ocr.Extract("SUMME 1 234,56").AmountCents; got != 123456 {
		t.Errorf("amount = %d, want 123456", got)
	}
}

func TestExtractIsPureText(t *testing.T) {
	// Extract must not need the binary: it is the tested half of the feature.
	d := ocr.Extract(strings.ToUpper(supermarktBon))
	if d.AmountCents != 1577 {
		t.Errorf("uppercase text broke extraction: %+v", d)
	}
}

// What `pdftotext -layout` makes of a typical supplier invoice: letterhead
// beside the title, the user's own business as recipient, several dates,
// and net and VAT subtotals above the gross total.
const layoutInvoice = `
Hosting Alpen GmbH                                        RECHNUNG
Serverweg 1, 6020 Innsbruck                     Rechnungsnummer: RE-2026/0815
UID: ATU11111111                                Lieferdatum: 01.08.2026
                                                Rechnungsdatum: 03.08.2026
Muster IT e.U.                                  Fällig: 17.08.2026
Beispielgasse 2
1010 Wien

Pos  Beschreibung                    Menge     Einzelpreis        Netto
1    Managed Server August               1          450,00       450,00

                                           Summe netto           450,00
                                           USt 20 %               90,00
                                           Gesamtbetrag          540,00
IBAN AT00 1111 2222 3333 4444
`

func TestExtractSupplierInvoice(t *testing.T) {
	d := ocr.Extract(layoutInvoice, "Muster IT e.U.")
	if d.AmountCents != 54000 {
		t.Errorf("amount = %d, want the gross total 54000, not a subtotal", d.AmountCents)
	}
	if d.BookedOn != "2026-08-03" {
		t.Errorf("date = %q, want the Rechnungsdatum, not the delivery date", d.BookedOn)
	}
	if d.Vendor != "Hosting Alpen GmbH" {
		t.Errorf("vendor = %q", d.Vendor)
	}
	if d.InvoiceNumber != "RE-2026/0815" {
		t.Errorf("invoice number = %q", d.InvoiceNumber)
	}
	if !d.VATRateRead || d.VATRate != 20 {
		t.Errorf("VAT = %d (read %v)", d.VATRate, d.VATRateRead)
	}
	if d.Description() != "Hosting Alpen GmbH – Rechnung RE-2026/0815" {
		t.Errorf("description = %q", d.Description())
	}
}

func TestOwnNameIsNotTheVendor(t *testing.T) {
	text := "Muster IT e.U.\nBeispielgasse 2\n\nPapier Profi KG\nSumme 12,00\n"
	if d := ocr.Extract(text, "Muster IT e.U."); d.Vendor != "Papier Profi KG" {
		t.Errorf("vendor = %q, want the supplier rather than the recipient", d.Vendor)
	}
	if d := ocr.Extract("Hotel Sacher\nSumme 12,00\n"); d.Vendor != "Hotel Sacher" {
		t.Errorf("vendor = %q — contact-line filtering must not eat 'Hotel'", d.Vendor)
	}
}

// testdata/invoice-layout.txt is `pdftotext -layout` of an invoice rendered by
// this app's own PDF writer (internal/invoice): the issuer's letterhead on the
// right, "EUR" after every amount. An incoming invoice from another user of
// this app is the most predictable PDF there is, so it had better be read.
func TestExtractInvoiceFromThisAppsPDFWriter(t *testing.T) {
	raw, err := os.ReadFile("testdata/invoice-layout.txt")
	if err != nil {
		t.Fatal(err)
	}
	d := ocr.Extract(string(raw), "Muster IT e.U.")
	if d.InvoiceNumber != "2026-0001" {
		t.Errorf("invoice number = %q, want the unlabelled title number", d.InvoiceNumber)
	}
	if d.AmountCents != 12000 || d.BookedOn != "2026-09-12" || d.Vendor != "Papier Profi KG" || !d.VATRateRead {
		t.Errorf("draft = amount %d, date %q, vendor %q, VAT read %v", d.AmountCents, d.BookedOn, d.Vendor, d.VATRateRead)
	}
}
