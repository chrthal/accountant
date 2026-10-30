package invoice_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/invoice"
)

// The layout is fixed by EPC069-12: banking apps read the lines by position.
func TestEPCPayload(t *testing.T) {
	got, err := invoice.EPCPayload("Max Mustermann", "AT61 1904 3002 3457 3201", "abcdatwwxxx", 170000, "Rechnung E260112001")
	if err != nil {
		t.Fatal(err)
	}
	want := "BCD\n002\n1\nSCT\nABCDATWWXXX\nMax Mustermann\nAT611904300234573201\nEUR1700.00\n\n\nRechnung E260112001"
	if got != want {
		t.Errorf("payload =\n%q\nwant\n%q", got, want)
	}

	long, err := invoice.EPCPayload(strings.Repeat("ä", 80), "AT611904300234573201", "", 5, "x")
	if err != nil {
		t.Fatal(err)
	}
	if name := strings.Split(long, "\n")[5]; len([]rune(name)) != 70 {
		t.Errorf("name has %d characters, the limit is 70", len([]rune(name)))
	}
	if !strings.Contains(long, "\n\n") || !strings.Contains(long, "EUR0.05") {
		t.Errorf("empty BIC or small amount wrong: %q", long)
	}

	for name, call := range map[string]func() (string, error){
		"short IBAN":  func() (string, error) { return invoice.EPCPayload("X", "AT12", "", 100, "") },
		"zero amount": func() (string, error) { return invoice.EPCPayload("X", "AT611904300234573201", "", 0, "") },
		"no name":     func() (string, error) { return invoice.EPCPayload(" ", "AT611904300234573201", "", 100, "") },
	} {
		if _, err := call(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// With a logo, an IBAN and a name outside cp1252, the PDF still renders. The
// font is embedded, so the name is printed as it is and not as "?".
func TestRenderPDFWithLogoAndQR(t *testing.T) {
	in := input(line("Website", 1000, 170000, 0))
	in.Kleinunternehmer = true
	in.Issuer.UID = ""
	in.Recipient.Name = "Łukasz Dvořák"
	in.PaymentIBAN, in.PaymentBIC = "AT611904300234573201", "ABCDATWWXXX"
	in.Contact = "Tel: +43 660 1234567\noffice@example.at"
	in.Note = "*Spezifikation\nStatische Website"
	doc, err := invoice.Build(in)
	if err != nil {
		t.Fatal(err)
	}

	img := image.NewNRGBA(image.Rect(0, 0, 60, 20))
	for x := 0; x < 60; x++ {
		img.Set(x, 10, color.NRGBA{255, 128, 0, 255})
	}
	var logo bytes.Buffer
	if err := png.Encode(&logo, img); err != nil {
		t.Fatal(err)
	}
	l := invoice.Logo{Data: logo.Bytes(), Type: "PNG"}
	if err := invoice.CheckLogo(l); err != nil {
		t.Fatalf("check logo: %v", err)
	}
	with, err := invoice.RenderPDF(doc, &l)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	without, err := invoice.RenderPDF(doc, nil)
	if err != nil {
		t.Fatalf("render without logo: %v", err)
	}
	if !bytes.Contains(with, []byte("/Subtype /Image")) || bytes.Contains(without, []byte("/Subtype /Image")) {
		t.Error("the logo is not embedded, or embedded without being given")
	}
	if !bytes.Contains(with, []byte("/FontFile2")) {
		t.Error("the font is not embedded")
	}

	if err := invoice.CheckLogo(invoice.Logo{Data: []byte("not an image"), Type: "PNG"}); err == nil {
		t.Error("a broken logo passed the check")
	}
}
