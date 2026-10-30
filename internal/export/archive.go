package export

import (
	"archive/zip"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
	"unicode"
)

// ArchiveFile is one file for the archive. Open is called only when the file
// is written, so a year of receipts is never held in memory at once.
type ArchiveFile struct {
	Path string
	Open func() ([]byte, error)
}

// ReceiptPath names a Beleg inside the archive: date and booking number
// first, so the folder sorts like the ledger, then the original file name.
// The attachment id keeps two files of the same name apart.
func ReceiptPath(year int, bookedOn string, entryID, attachmentID int64, filename string) string {
	name := safeName(filename)
	if name == "" {
		name = "beleg"
	}
	return fmt.Sprintf("%s/Belege/%s_Buchung-%d_%d_%s", ArchiveRoot(year), bookedOn, entryID, attachmentID, name)
}

// InvoicePath names an issued invoice inside the archive.
func InvoicePath(year int, number string) string {
	return fmt.Sprintf("%s/Rechnungen/Rechnung-%s.pdf", ArchiveRoot(year), safeName(number))
}

// ArchiveRoot is the one folder everything in the archive sits in, so it
// unpacks into a single directory.
func ArchiveRoot(year int) string { return fmt.Sprintf("kmu-buchhaltung-%d", year) }

// WriteArchive writes the year as a ZIP that needs no software of ours to
// read (ADR 0029): the bookings CSV, every Beleg of those bookings, every
// invoice issued in the year as PDF, and a note that explains the layout.
func WriteArchive(w io.Writer, year int, bookingsCSV []byte, receipts, invoices []ArchiveFile, made time.Time) error {
	zw := zip.NewWriter(w)
	add := func(name string, data []byte) error {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: made})
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}
	root := ArchiveRoot(year)
	if err := add(path.Join(root, "LIESMICH.txt"), []byte(readme(year, len(receipts), len(invoices), made))); err != nil {
		return err
	}
	if err := add(path.Join(root, FileName("Buchungen", year, "csv")), bookingsCSV); err != nil {
		return err
	}
	for _, list := range [][]ArchiveFile{receipts, invoices} {
		for _, f := range list {
			data, err := f.Open()
			if err != nil {
				return fmt.Errorf("%s: %w", f.Path, err)
			}
			if err := add(f.Path, data); err != nil {
				return err
			}
		}
	}
	return zw.Close()
}

func readme(year, receipts, invoices int, made time.Time) string {
	return fmt.Sprintf(`kmu-buchhaltung: Archiv %d
Erstellt am %s.

Buchungen-%d.csv
  Alle Buchungen des Jahres (Einnahmen und Ausgaben), mit Stornos, und eine
  USt-Zusammenfassung. Trennzeichen Semikolon, UTF-8; öffnet sich direkt in
  Excel oder LibreOffice. Die Spalte "Belege" nennt die Dateien im Ordner
  Belege, die zu der Buchung gehören.

Belege/ (%d Dateien)
  Die Belege zu den Buchungen, so wie sie hochgeladen wurden.
  Dateiname: Belegdatum_Buchung-Nummer_Dateinummer_Originalname.

Rechnungen/ (%d Dateien)
  Alle im Jahr ausgestellten Rechnungen als PDF.

Belege und Rechnungen sind sieben Jahre aufzubewahren (§ 132 BAO).
Dieses Archiv ist zum Lesen und Weitergeben gedacht. Zum Wiederherstellen
der App dient die Datensicherung (Einstellungen, Datensicherung).
`, year, made.Format("02.01.2006 15:04"), year, receipts, invoices)
}

// safeName keeps a file name readable on every system: letters, digits and
// ".-_" stay, everything else becomes "_".
func safeName(s string) string {
	s = strings.TrimSpace(path.Base(strings.ReplaceAll(s, "\\", "/")))
	if s == "." || s == "/" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if r := []rune(out); len(r) > 80 {
		out = string(r[len(r)-80:])
	}
	return out
}
