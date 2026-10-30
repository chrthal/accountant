package ocr_test

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"

	"github.com/chrthal/accountant/internal/ocr"
)

// textPDF renders a small supplier invoice with a real text layer, the way
// accounting software produces them.
func textPDF(t *testing.T) []byte {
	t.Helper()
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Helvetica", "B", 14)
	pdf.CellFormat(100, 8, "Hosting Alpen GmbH", "", 0, "L", false, 0, "")
	pdf.CellFormat(80, 8, "RECHNUNG", "", 1, "R", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	for _, l := range []string{
		"Rechnungsnummer: RE-2026/0815",
		"Lieferdatum: 01.08.2026",
		"Rechnungsdatum: 03.08.2026",
		"Managed Server August            450,00",
		"Summe netto                      450,00",
		"USt 20 %                          90,00",
		"Gesamtbetrag                     540,00",
		"Zahlbar innerhalb von 14 Tagen ohne Abzug.",
	} {
		pdf.CellFormat(180, 6, tr(l), "", 1, "L", false, 0, "")
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// scannedPDF wraps the receipt photo in a PDF without any text: what a
// scanner app or a "print to PDF" of a photo produces.
func scannedPDF(t *testing.T) []byte {
	t.Helper()
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.ImageOptions("testdata/receipt.png", 20, 20, 120, 0, false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func have(bin string) bool { _, err := exec.LookPath(bin); return err == nil }

func TestReadPDFWithTextLayer(t *testing.T) {
	if !have("pdftotext") {
		t.Skip("pdftotext (poppler) is not installed — PDFs are then stored without pre-fill (ADR 0018)")
	}
	text, method, err := ocr.Read(context.Background(), textPDF(t), "rechnung.pdf")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if method != "text layer" {
		t.Errorf("method = %q — a digital PDF must be read from its text, not OCR'd", method)
	}
	d := ocr.Extract(text)
	if d.AmountCents != 54000 || d.BookedOn != "2026-08-03" || d.InvoiceNumber != "RE-2026/0815" {
		t.Errorf("draft = amount %d, date %q, nr %q\n%s", d.AmountCents, d.BookedOn, d.InvoiceNumber, text)
	}
	if !strings.Contains(d.Vendor, "Hosting Alpen GmbH") {
		t.Errorf("vendor = %q", d.Vendor)
	}
}

func TestReadScannedPDFFallsBackToOCR(t *testing.T) {
	if !have("pdftoppm") || !ocr.Available() {
		t.Skip("pdftoppm and tesseract are both needed to read a scanned PDF")
	}
	text, method, err := ocr.Read(context.Background(), scannedPDF(t), "scan.pdf")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if method != "OCR of page 1" {
		t.Errorf("method = %q, want the OCR fallback", method)
	}
	if d := ocr.Extract(text); d.AmountCents != 1300 {
		t.Errorf("amount from the scan = %d, want 1300\n%s", d.AmountCents, text)
	}
}

func TestReadRefusesWhatItCannotRead(t *testing.T) {
	if _, _, err := ocr.Read(context.Background(), append([]byte("%PDF-1.4\n"), make([]byte, ocr.MaxImageSize)...), "big.pdf"); err == nil {
		t.Error("an oversized PDF was accepted")
	}
	if !ocr.IsPDF([]byte("%PDF-1.7")) || ocr.IsPDF([]byte("\x89PNG")) {
		t.Error("IsPDF misjudges the magic bytes")
	}
	// A broken PDF is an error, not a hang or a crash.
	if have("pdftotext") {
		if _, _, err := ocr.Read(context.Background(), []byte("%PDF-1.4\nnot really a pdf"), "x.pdf"); err == nil {
			t.Log("a broken PDF produced no error (poppler tolerated it) — acceptable, as long as it returns")
		}
	}
}
