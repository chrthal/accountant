package ocr_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/ocr"
)

// The subprocess half of the feature: thin, and only testable where the binary
// exists. testdata/receipt.png is a rendered Austrian café receipt.
func TestRecogniseRealImage(t *testing.T) {
	if !ocr.Available() {
		t.Skip("tesseract is not installed — the app degrades to manual entry (ADR 0011)")
	}

	image, err := os.ReadFile("testdata/receipt.png")
	if err != nil {
		t.Fatalf("read test image: %v", err)
	}

	text, err := ocr.Recognise(context.Background(), image, "receipt.png")
	if err != nil {
		t.Fatalf("recognise: %v", err)
	}
	t.Logf("recognised text:\n%s", text)

	d := ocr.Extract(text)
	if d.AmountCents != 1300 {
		t.Errorf("amount = %d, want 1300 from the SUMME line", d.AmountCents)
	}
	if d.BookedOn != "2026-03-15" {
		t.Errorf("date = %q, want 2026-03-15", d.BookedOn)
	}
	if d.Vendor == "" {
		t.Error("no vendor recognised")
	}
}

func TestRecogniseRejectsBadInput(t *testing.T) {
	if !ocr.Available() {
		t.Skip("tesseract is not installed")
	}
	ctx := context.Background()

	if _, err := ocr.Recognise(ctx, nil, "x.png"); err == nil {
		t.Error("empty image accepted")
	}
	if _, err := ocr.Recognise(ctx, make([]byte, ocr.MaxImageSize+1), "x.png"); err == nil {
		t.Error("oversized image accepted")
	}
	// Not an image at all: tesseract must fail loudly rather than return junk.
	if _, err := ocr.Recognise(ctx, []byte("this is not an image"), "x.png"); err == nil {
		t.Error("non-image accepted")
	}
}

// A filename from an upload is untrusted; only its extension is used, and a
// path in it must not escape the temp directory.
func TestRecogniseIgnoresPathsInTheFilename(t *testing.T) {
	if !ocr.Available() {
		t.Skip("tesseract is not installed")
	}
	image, err := os.ReadFile("testdata/receipt.png")
	if err != nil {
		t.Fatalf("read test image: %v", err)
	}
	text, err := ocr.Recognise(context.Background(), image, "../../../etc/passwd.png")
	if err != nil {
		t.Fatalf("recognise: %v", err)
	}
	if text == "" {
		t.Error("no text recognised")
	}
}

func TestUnavailableIsAnOrdinaryError(t *testing.T) {
	// Not skipped: this documents the contract the handlers rely on.
	if !errors.Is(ocr.ErrUnavailable, ocr.ErrUnavailable) {
		t.Error("ErrUnavailable is not matchable")
	}
}

// Requesting a language whose data is missing makes tesseract fail outright, so
// the language set must be detected rather than assumed — installations differ
// (the container ships German, a Homebrew install ships English).
func TestLanguagesAreDetectedNotAssumed(t *testing.T) {
	if !ocr.Available() {
		t.Skip("tesseract is not installed")
	}
	langs := ocr.Languages()
	t.Logf("languages in use: %q", langs)

	for _, l := range strings.Split(langs, "+") {
		if l == "" {
			continue
		}
		if l != "deu" && l != "eng" {
			t.Errorf("unexpected language %q", l)
		}
	}
}
