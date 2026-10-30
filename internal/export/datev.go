package export

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// DATEVConfig holds the values the EXTF format needs that this app cannot
// invent. They come from the Steuerberater (ADR 0010) and the export refuses to
// run without them: a Buchungsstapel carrying guessed numbers would import into
// the wrong client's books.
type DATEVConfig struct {
	ConsultantNumber string // Berater-Nummer
	ClientNumber     string // Mandanten-Nummer
	AccountLength    int    // Sachkontenlänge
	FiscalYearStart  string // 'YYYY-MM-DD'
}

// Validate reports what is still missing before an EXTF file can be written.
func (c DATEVConfig) Validate() error {
	if strings.TrimSpace(c.ConsultantNumber) == "" {
		return fmt.Errorf("Berater-Nummer fehlt — sie kommt von der Steuerberatung")
	}
	if strings.TrimSpace(c.ClientNumber) == "" {
		return fmt.Errorf("Mandanten-Nummer fehlt — sie kommt von der Steuerberatung")
	}
	if c.AccountLength < 4 || c.AccountLength > 9 {
		return fmt.Errorf("Sachkontenlänge muss zwischen 4 und 9 liegen")
	}
	if _, err := time.Parse("2006-01-02", c.FiscalYearStart); err != nil {
		return fmt.Errorf("Wirtschaftsjahresbeginn muss JJJJ-MM-TT sein")
	}
	return nil
}

// DATEV writes an EXTF Buchungsstapel for one year.
//
// NOT VALIDATED against DATEV's DatevFormatPrüfProgramm (ADR 0003 makes that the
// criterion for this being finished, and it is Windows software that is not
// available here). The structure follows the documented format: an EXTF header
// line, a column-header row, then one row per booking. Output is cp1252, since
// DATEV does not read UTF-8.
func DATEV(bookings []Booking, cfg DATEVConfig, year int, generatedAt time.Time) ([]byte, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if missing := MissingAccounts(bookings); len(missing) > 0 {
		return nil, fmt.Errorf("%d Buchung(en) ohne Konto/Gegenkonto: %s — "+
			"über die Kategorie hinterlegen oder bei der Buchung angeben",
			len(missing), joinIDs(missing))
	}

	var b strings.Builder

	// --- header line ------------------------------------------------------
	fiscalStart, _ := time.Parse("2006-01-02", cfg.FiscalYearStart)
	header := []string{
		`"EXTF"`,                                // format marker
		"700",                                   // version
		"21",                                    // category: Buchungsstapel
		`"Buchungsstapel"`,                      //
		"13",                                    // format version
		generatedAt.Format("20060102150405000"), // created at
		"",                                      // imported (empty)
		`"KB"`,                                  // origin
		`"kmu-buchhaltung"`,                     // exported by
		`""`,                                    // imported by
		cfg.ConsultantNumber,                    // Berater
		cfg.ClientNumber,                        // Mandant
		fiscalStart.Format("20060102"),          // Wirtschaftsjahresbeginn
		fmt.Sprint(cfg.AccountLength),           // Sachkontenlänge
		fmt.Sprintf("%d0101", year),             // period from
		fmt.Sprintf("%d1231", year),             // period to
		fmt.Sprintf(`"Buchungen %d"`, year),     // description
		`""`,                                    // Diktatkürzel
		"1",                                     // Buchungstyp: Finanzbuchführung
		"",                                      // Rechnungslegungszweck
		"0",                                     // Festschreibung: nein
		`"EUR"`,                                 // currency
		"", "", "", "", "", "", "", "", "", "",  // reserved / unused fields
	}
	b.WriteString(strings.Join(header, ";"))
	b.WriteString("\r\n")

	// --- column header row -------------------------------------------------
	columns := []string{
		"Umsatz (ohne Soll/Haben-Kz)", "Soll/Haben-Kennzeichen", "WKZ Umsatz",
		"Kurs", "Basis-Umsatz", "WKZ Basis-Umsatz", "Konto",
		"Gegenkonto (ohne BU-Schlüssel)", "BU-Schlüssel", "Belegdatum",
		"Belegfeld 1", "Belegfeld 2", "Skonto", "Buchungstext",
	}
	for i, c := range columns {
		if i > 0 {
			b.WriteString(";")
		}
		b.WriteString(`"` + c + `"`)
	}
	b.WriteString("\r\n")

	// --- bookings ----------------------------------------------------------
	for _, bk := range bookings {
		// A Storno is a real booking and must be exported; the Soll/Haben
		// indicator already carries its opposite direction.
		date, err := time.Parse("2006-01-02", bk.BookedOn)
		if err != nil {
			return nil, fmt.Errorf("booking %d: %w", bk.ID, err)
		}

		// Income debits the account, an outgoing credits it. Whether that
		// orientation matches the Steuerberater's framework is part of what the
		// Prüfprogramm run has to confirm (ADR 0010).
		sollHaben := "S"
		if bk.Direction == "outgoing" {
			sollHaben = "H"
		}

		row := []string{
			datevAmount(bk.AmountGrossCents), // Umsatz: decimal comma, no grouping, unsigned
			sollHaben,
			`"EUR"`,
			"", "", "", // Kurs, Basis-Umsatz, WKZ Basis-Umsatz
			quote(bk.Konto),
			quote(bk.Gegenkonto),
			"",                                 // BU-Schlüssel: the VAT key is part of the account framework
			date.Format("0201"),                // Belegdatum DDMM ("02" is the day, "01" the month)
			quote(fmt.Sprint(bk.ID)),           // Belegfeld 1: our entry number
			`""`,                               // Belegfeld 2
			"",                                 // Skonto
			quote(truncate(datevText(bk), 60)), // Buchungstext, max 60 chars
		}
		b.WriteString(strings.Join(row, ";"))
		b.WriteString("\r\n")
	}

	return encodeCP1252(b.String()), nil
}

// datevAmount formats cents the way EXTF wants them: a decimal comma and no
// thousands separator. money.Format groups thousands for display, which DATEV
// rejects.
func datevAmount(cents int64) string {
	if cents < 0 {
		cents = -cents
	}
	return fmt.Sprintf("%d,%02d", cents/100, cents%100)
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// truncate cuts to n runes — DATEV's Buchungstext is limited to 60 characters,
// and cutting bytes would split a multi-byte character.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// encodeCP1252 converts UTF-8 to cp1252, which is what DATEV reads. Characters
// outside cp1252 become '?' — visible in the file rather than silently dropped,
// and booking texts are typed by the user, so they can fix them.
func encodeCP1252(s string) []byte {
	var buf bytes.Buffer
	for _, r := range s {
		switch {
		case r < 0x100:
			buf.WriteByte(byte(r)) // latin-1 range maps 1:1
		case r == '€':
			buf.WriteByte(0x80)
		case r == '‚':
			buf.WriteByte(0x82)
		case r == '„':
			buf.WriteByte(0x84)
		case r == '…':
			buf.WriteByte(0x85)
		case r == '–':
			buf.WriteByte(0x96)
		case r == '—':
			buf.WriteByte(0x97)
		case r == '“':
			buf.WriteByte(0x93)
		case r == '”':
			buf.WriteByte(0x94)
		default:
			buf.WriteByte('?')
		}
	}
	return buf.Bytes()
}

// MissingAccounts returns the ids of bookings that carry no Konto or no
// Gegenkonto, oldest first.
//
// Account tags are optional on a booking (ADR 0012) because cash-basis
// bookkeeping does not need them — but EXTF is a double-entry format and every
// row needs both. The requirement therefore lives here, at the export, and the
// reports page shows the list so the user can fix the categories rather than
// hunt for which booking was refused.
func MissingAccounts(bookings []Booking) []int64 {
	var out []int64
	for _, b := range bookings {
		if strings.TrimSpace(b.Konto) == "" || strings.TrimSpace(b.Gegenkonto) == "" {
			out = append(out, b.ID)
		}
	}
	return out
}

func joinIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = "#" + fmt.Sprint(id)
	}
	return strings.Join(parts, ", ")
}

// datevText puts a mixed expense's business share first, where truncation
// cannot cut it off. The amount stays in full: the VAT key comes from the
// account framework, and the private share needs the Steuerberater's
// accounts (ADR 0041).
func datevText(b Booking) string {
	if b.Direction == "outgoing" && b.mixed() {
		return fmt.Sprintf("Betriebl. %d%%: %s", b.pct(), b.Description)
	}
	return b.Description
}
