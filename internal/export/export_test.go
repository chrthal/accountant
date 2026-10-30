package export_test

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chrthal/accountant/internal/export"
)

func bookings() []export.Booking {
	reversed := int64(1)
	return []export.Booking{
		{ID: 1, BookedOn: "2026-03-15", Direction: "outgoing", Description: "Büromaterial",
			AmountGrossCents: 12000, VATRate: 20, VATCents: 2000, NetCents: 10000,
			Konto: "7600", Gegenkonto: "2800", ContactName: "Büro Müller GmbH", IsReversed: true},
		{ID: 2, BookedOn: "2026-03-16", Direction: "income", Description: "Honorar März",
			AmountGrossCents: 200000, VATRate: 20, VATCents: 33333, NetCents: 166667,
			Konto: "2000", Gegenkonto: "4000", ContactName: "Muster GmbH"},
		{ID: 3, BookedOn: "2026-03-17", Direction: "income", Description: "Storno zu #1: Büromaterial",
			AmountGrossCents: 12000, VATRate: 20, VATCents: 2000, NetCents: 10000,
			Konto: "2800", Gegenkonto: "7600", IsReversal: true, ReversesEntryID: &reversed},
	}
}

func TestCSVExport(t *testing.T) {
	out := export.CSV(bookings())

	if !bytes.HasPrefix(out, []byte{0xEF, 0xBB, 0xBF}) {
		t.Error("no UTF-8 BOM — Excel in a German locale will mangle the umlauts")
	}
	text := string(out)

	if !strings.Contains(text, "Belegdatum;Art") {
		t.Error("header row missing")
	}
	// Austrian amount notation, and Konto/Gegenkonto on every row.
	if !strings.Contains(text, "120,00") || !strings.Contains(text, "2.000,00") {
		t.Errorf("amounts are not in Austrian notation:\n%s", text)
	}
	if !strings.Contains(text, "7600;2800") {
		t.Error("Konto/Gegenkonto missing from the rows")
	}
	// Corrections are part of the record, marked rather than filtered out.
	if !strings.Contains(text, "Storno zu 1") {
		t.Error("the Storno relationship is not exported")
	}
	if !strings.Contains(text, "USt-Zusammenfassung") {
		t.Error("VAT summary missing")
	}

	// The reversed pair must not appear in the summary: it nets to zero, and
	// stating its VAT would claim tax that was never owed.
	summary := text[strings.Index(text, "USt-Zusammenfassung"):]
	if strings.Contains(summary, "Ausgaben") {
		t.Errorf("reversed expense counted in the VAT summary:\n%s", summary)
	}
	if !strings.Contains(summary, "Einnahmen;20%;1.666,67;333,33;2.000,00") {
		t.Errorf("income summary line wrong:\n%s", summary)
	}
}

func datevConfig() export.DATEVConfig {
	return export.DATEVConfig{
		ConsultantNumber: "1234567", ClientNumber: "78901",
		AccountLength: 4, FiscalYearStart: "2026-01-01",
	}
}

func TestDATEVStructure(t *testing.T) {
	out, err := export.DATEV(bookings(), datevConfig(), 2026,
		time.Date(2026, 8, 18, 9, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(out), "\r\n"), "\r\n")
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want header + columns + 3 bookings", len(lines))
	}

	if !strings.HasPrefix(lines[0], `"EXTF";700;21;"Buchungsstapel"`) {
		t.Errorf("header line does not start with the EXTF marker: %s", lines[0])
	}
	for _, want := range []string{"1234567", "78901", "20260101", "20260101;20261231", `"EUR"`} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("header is missing %q: %s", want, lines[0])
		}
	}
	if !strings.HasPrefix(lines[1], `"Umsatz (ohne Soll/Haben-Kz)";"Soll/Haben-Kennzeichen"`) {
		t.Errorf("column header row wrong: %s", lines[1])
	}

	// An outgoing is H, an income S, amounts unsigned with a decimal comma, and
	// the date is DDMM.
	first := strings.Split(lines[2], ";")
	if first[0] != "120,00" {
		t.Errorf("amount = %q, want 120,00 unsigned with a decimal comma", first[0])
	}
	if first[1] != "H" {
		t.Errorf("Soll/Haben for an outgoing = %q, want H", first[1])
	}
	if first[9] != "1503" {
		t.Errorf("Belegdatum = %q, want DDMM (1503)", first[9])
	}
	if second := strings.Split(lines[3], ";"); second[1] != "S" {
		t.Errorf("Soll/Haben for an income = %q, want S", second[1])
	}

	// Every row must carry both accounts — that is the whole reason ADR 0003
	// put them on entries.
	for i, l := range lines[2:] {
		f := strings.Split(l, ";")
		if f[6] == `""` || f[7] == `""` {
			t.Errorf("booking row %d has an empty Konto or Gegenkonto: %s", i+1, l)
		}
	}
}

// DATEV reads cp1252, not UTF-8.
func TestDATEVEncodesCP1252(t *testing.T) {
	b := bookings()
	b[0].Description = "Büromaterial für März"
	out, err := export.DATEV(b, datevConfig(), 2026, time.Now())
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	if bytes.Contains(out, []byte("Büromaterial")) {
		t.Error("output is UTF-8 — DATEV expects cp1252")
	}
	// ü is a single 0xFC byte in cp1252.
	if !bytes.Contains(out, []byte{'B', 0xFC, 'r', 'o'}) {
		t.Error("umlaut not encoded as cp1252")
	}
}

func TestDATEVTruncatesBookingText(t *testing.T) {
	b := bookings()
	b[0].Description = strings.Repeat("ä", 100)
	out, err := export.DATEV(b, datevConfig(), 2026, time.Now())
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	line := strings.Split(string(out), "\r\n")[2]
	text := line[strings.LastIndex(line, ";")+1:]
	if len([]byte(text)) != 62 { // 60 characters plus the surrounding quotes
		t.Errorf("Buchungstext is %d bytes, want 60 characters quoted", len([]byte(text)))
	}
}

// Guessed Berater-/Mandanten numbers would import into the wrong books, so the
// export must refuse rather than default them (ADR 0010).
func TestDATEVRefusesIncompleteConfig(t *testing.T) {
	cases := map[string]func(c *export.DATEVConfig){
		"no consultant number": func(c *export.DATEVConfig) { c.ConsultantNumber = "" },
		"no client number":     func(c *export.DATEVConfig) { c.ClientNumber = " " },
		"bad account length":   func(c *export.DATEVConfig) { c.AccountLength = 0 },
		"bad fiscal year":      func(c *export.DATEVConfig) { c.FiscalYearStart = "01.01.2026" },
	}
	for name, mutate := range cases {
		cfg := datevConfig()
		mutate(&cfg)
		if _, err := export.DATEV(bookings(), cfg, 2026, time.Now()); err == nil {
			t.Errorf("%s: export succeeded, want a refusal", name)
		}
	}
}

func TestFileName(t *testing.T) {
	if got := export.FileName("Buchungen", 2026, "csv"); got != "Buchungen-2026.csv" {
		t.Errorf("FileName = %q", got)
	}
}

// EXTF amounts carry a decimal comma but no thousands separator — the display
// formatter groups them, which DATEV rejects.
func TestDATEVAmountsHaveNoThousandsSeparator(t *testing.T) {
	b := bookings()
	b[0].AmountGrossCents = 4_000_000 // 40.000,00 when displayed
	out, err := export.DATEV(b, datevConfig(), 2026, time.Now())
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	line := strings.Split(string(out), "\r\n")[2]
	if got := strings.Split(line, ";")[0]; got != "40000,00" {
		t.Errorf("amount = %q, want 40000,00 without grouping", got)
	}
}

// Account tags are optional on a booking (ADR 0012), but EXTF is a
// double-entry format and cannot represent a row without them. The refusal has
// to name the offending bookings — "some entry somewhere" is not actionable in
// a ledger the user cannot edit.
func TestDATEVRefusesBookingsWithoutAccounts(t *testing.T) {
	bs := bookings()
	bs[0].Konto = ""
	bs[1].Gegenkonto = "   "

	missing := export.MissingAccounts(bs)
	if len(missing) != 2 {
		t.Fatalf("MissingAccounts = %v, want both bookings", missing)
	}

	_, err := export.DATEV(bs, datevConfig(), 2026, time.Now())
	if err == nil {
		t.Fatal("export succeeded with blank account tags, want a refusal")
	}
	for _, want := range []string{
		"#" + strconv.FormatInt(bs[0].ID, 10),
		"#" + strconv.FormatInt(bs[1].ID, 10),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name booking %s", err, want)
		}
	}

	// Complete bookings still export.
	if _, err := export.DATEV(bookings(), datevConfig(), 2026, time.Now()); err != nil {
		t.Errorf("complete bookings refused: %v", err)
	}
}
