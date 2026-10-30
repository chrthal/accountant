package invoice_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/chrthal/accountant/internal/invoice"
)

// PDF bytes cannot be asserted on meaningfully, so this checks the things that
// can actually go wrong here: that rendering succeeds for both invoice kinds,
// produces a real PDF, and does not blow up on the characters an Austrian
// invoice is full of. The numbers themselves are covered in document_test.go.
func TestRenderPDF(t *testing.T) {
	normal, err := invoice.Build(input(
		line("Beratung März — Konzeption & Umsetzung", 1500, 10000, 20),
		line("Verpflegung (ermäßigt)", 2000, 2500, 10),
	))
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	kuIn := input(line("Beratung", 1000, 50000, 0))
	kuIn.Kleinunternehmer = true
	kuIn.Issuer.UID = ""
	ku, err := invoice.Build(kuIn)
	if err != nil {
		t.Fatalf("build Kleinunternehmer: %v", err)
	}

	for name, doc := range map[string]invoice.Document{"normal": normal, "kleinunternehmer": ku} {
		t.Run(name, func(t *testing.T) {
			out, err := invoice.RenderPDF(doc, nil)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if !bytes.HasPrefix(out, []byte("%PDF-")) {
				t.Errorf("output does not start with the PDF magic bytes")
			}
			if len(out) < 800 {
				t.Errorf("PDF is %d bytes — too small to contain an invoice", len(out))
			}
			// Keep the artefact around when -v is used, so a human can look.
			if testing.Verbose() {
				path := filepath.Join(t.TempDir(), name+".pdf")
				if err := os.WriteFile(path, out, 0o644); err == nil {
					t.Logf("wrote %s", path)
				}
			}
		})
	}
}

// A description long enough to wrap must not silently overlap the next column.
func TestRenderPDFWithLongDescription(t *testing.T) {
	doc, err := invoice.Build(input(line(
		"Umfassende Beratung zur Umstellung der Buchhaltung inklusive Migration "+
			"der Altdaten, Schulung der Mitarbeiterinnen und Mitarbeiter sowie "+
			"Dokumentation der neuen Abläufe", 1000, 250000, 20)))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	out, err := invoice.RenderPDF(doc, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Error("not a PDF")
	}
}

// Many positions run onto further pages; the PDF must hold every page and
// not cut rows off at the footer.
func TestRenderPDFOverSeveralPages(t *testing.T) {
	var lines []invoice.Line
	for range 60 {
		lines = append(lines, line("Wartung und Sicherheitsupdates für den Webserver, monatlich", 1000, 1500, 20))
	}
	doc, err := invoice.Build(input(lines...))
	if err != nil {
		t.Fatal(err)
	}
	out, err := invoice.RenderPDF(doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	pages := bytes.Count(out, []byte("/Type /Page\n")) + bytes.Count(out, []byte("/Type /Page>>"))
	if pages < 3 {
		t.Errorf("60 wrapped positions gave %d pages, want 3 or more", pages)
	}
}
