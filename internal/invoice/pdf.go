package invoice

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"

	"github.com/chrthal/accountant/internal/money"
	"github.com/go-pdf/fpdf"
	"rsc.io/qr"
)

// Logo is an image for the top of the invoice. It is not part of the
// document's content: every PDF is drawn with the current logo (ADR 0025).
type Logo struct {
	Data []byte
	Type string // "PNG" or "JPG", as fpdf names them
}

// CheckLogo reports whether fpdf can draw the image, so a bad file is refused
// on upload and not when an invoice is printed.
func CheckLogo(l Logo) error {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.RegisterImageOptionsReader("logo", fpdf.ImageOptions{ImageType: l.Type}, bytes.NewReader(l.Data))
	return pdf.Error()
}

// The font is embedded. With fpdf's built-in Helvetica the viewer supplies the
// glyphs, and macOS draws "€" wider than the metrics fpdf used, so the next
// word ran into it. An embedded font also covers names outside cp1252.
const font = "dejavu"

var (
	//go:embed fonts/DejaVuSansCondensed.ttf
	fontRegular []byte
	//go:embed fonts/DejaVuSansCondensed-Bold.ttf
	fontBold []byte
)

const (
	pageW    = 210.0
	marginL  = 20.0
	contentW = pageW - 2*marginL
	rightX   = 125.0 // the right-hand column: issuer and details
	rightW   = pageW - marginL - rightX
	footerH  = 24.0
)

// RenderPDF draws the document onto A4 pages and returns the PDF bytes. logo
// may be nil.
//
// This file only draws: every amount it prints was computed and checked by
// Build (ADR 0008).
func RenderPDF(doc Document, logo *Logo) ([]byte, error) {
	// The first pass only counts pages. "Seite 1 von 3" needs the total
	// before page 1 is drawn, and a one-page invoice gets no page numbers.
	pdf, err := render(doc, logo, 0)
	if err != nil {
		return nil, err
	}
	if n := pdf.PageCount(); n > 1 {
		if pdf, err = render(doc, logo, n); err != nil {
			return nil, err
		}
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("render invoice %s: %w", doc.Number, err)
	}
	return buf.Bytes(), nil
}

// bottom is the lowest y that body content may reach.
const bottom = 297 - footerH - 6

func render(doc Document, logo *Logo, pages int) (*fpdf.Fpdf, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes(font, "", fontRegular)
	pdf.AddUTF8FontFromBytes(font, "B", fontBold)
	pdf.SetMargins(marginL, 18, marginL)
	pdf.SetAutoPageBreak(true, 297-bottom)
	pdf.SetHeaderFunc(func() { drawHeader(pdf, doc, pages) })
	pdf.SetFooterFunc(func() { drawFooter(pdf, doc) })
	pdf.AddPage()

	if logo != nil && len(logo.Data) > 0 {
		pdf.RegisterImageOptionsReader("logo", fpdf.ImageOptions{ImageType: logo.Type}, bytes.NewReader(logo.Data))
		if info := pdf.GetImageInfo("logo"); info != nil && pdf.Error() == nil {
			w, h := fit(info.Width(), info.Height(), 75, 22)
			pdf.ImageOptions("logo", marginL, 18, w, h, false, fpdf.ImageOptions{ImageType: logo.Type}, 0, "")
		}
	}
	pdf.SetXY(marginL, 22)
	pdf.SetFont(font, "", 22)
	title := "Rechnung"
	if doc.Draft {
		title = "Rechnungsentwurf"
	}
	pdf.CellFormat(contentW, 10, title, "", 2, "R", false, 0, "")
	if pages > 1 {
		pdf.SetFont(font, "", 8)
		pdf.SetTextColor(110, 110, 110)
		pdf.CellFormat(contentW, 5, fmt.Sprintf("Seite 1 von %d", pages), "", 0, "R", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
	}

	// --- recipient left, issuer and details right -------------------------
	top := 50.0
	pdf.SetFont(font, "", 10)
	pdf.SetXY(marginL, top)
	party(pdf, marginL, 90, doc.Recipient)
	leftEnd := pdf.GetY()

	pdf.SetXY(rightX, top)
	party(pdf, rightX, rightW, doc.Issuer)
	pdf.SetXY(rightX, pdf.GetY()+5)
	pdf.SetFont(font, "B", 10)
	pdf.CellFormat(rightW, 5, "Details", "", 2, "L", false, 0, "")
	pdf.SetFont(font, "", 10)
	details := [][2]string{
		{"Rechnungsnummer", doc.Number},
		{"Rechnungsdatum", formatDate(doc.IssueDate)},
		{"Leistungsdatum", formatDate(doc.ServiceDate)},
	}
	if doc.DueDate != "" {
		details = append(details, [2]string{"Zahlbar bis", formatDate(doc.DueDate)})
	}
	for _, d := range details {
		y := pdf.GetY()
		pdf.SetXY(rightX, y)
		pdf.CellFormat(rightW, 5, d[0], "", 0, "L", false, 0, "")
		pdf.SetXY(rightX, y)
		pdf.CellFormat(rightW, 5, d[1], "", 2, "R", false, 0, "")
	}
	pdf.SetXY(marginL, max(leftEnd, pdf.GetY())+14)

	// --- line items --------------------------------------------------------
	w := []float64{46, 18, 23, 27, 14, 18, 24}
	headers := []string{"Bezeichnung", "Menge", "Preis p. E.", "Zwischensumme", "USt. %", "USt.", "Gesamt"}
	aligns := []string{"L", "R", "R", "R", "R", "R", "R"}
	drawTableHeader(pdf, w, headers, aligns)

	var carriedNet, carriedVAT, carriedGross int64
	for _, l := range doc.Lines {
		qty := money.FormatQuantity(l.QuantityMilli)
		if l.Unit != "" {
			qty += " " + l.Unit
		}
		rate, vat := "-", "-"
		if !doc.Kleinunternehmer {
			rate, vat = fmt.Sprintf("%d %%", l.VATRate), eur(l.VATCents)
		}
		desc := l.Description
		h := float64(max(1, len(pdf.SplitLines([]byte(desc), w[0]-1)))) * 5

		// A row is drawn cell by cell from one baseline, so a description that
		// wraps grows the row downwards instead of shifting the other columns.
		// Start a new page first if the whole row would not fit.
		if pdf.GetY()+h+2 > bottom {
			pdf.AddPage()
			drawTableHeader(pdf, w, headers, aligns)
			drawCarried(pdf, w, doc.Kleinunternehmer, carriedNet, carriedVAT, carriedGross)
		}
		y := pdf.GetY() + 1
		pdf.SetXY(marginL, y)
		pdf.MultiCell(w[0]-1, 5, desc, "", "L", false)
		x := marginL + w[0]
		for i, c := range []string{qty, eur(l.UnitPriceCents), eur(l.NetCents), rate, vat, eur(l.GrossCents)} {
			pdf.SetXY(x, y)
			pdf.CellFormat(w[i+1], 5, c, "", 0, aligns[i+1], false, 0, "")
			x += w[i+1]
		}
		pdf.SetDrawColor(200, 200, 200)
		pdf.Line(marginL, y+h+1, marginL+contentW, y+h+1)
		pdf.SetDrawColor(0, 0, 0)
		pdf.SetXY(marginL, y+h+1)
		carriedNet, carriedVAT, carriedGross = carriedNet+l.NetCents, carriedVAT+l.VATCents, carriedGross+l.GrossCents
	}

	// --- totals, with the exemption note beside them ------------------------
	if pdf.GetY()+30 > bottom {
		pdf.AddPage()
	}
	y := pdf.GetY() + 10
	if doc.Kleinunternehmer {
		pdf.SetXY(marginL, y+5)
		pdf.SetFont(font, "B", 8)
		pdf.MultiCell(65, 4, KleinunternehmerNote, "", "L", false)
	}
	leftEnd = pdf.GetY()

	tx, tw := 95.0, []float64{29, 22, 22, 22}
	cell := func(i int, s, border string, ln int) {
		pdf.CellFormat(tw[i], 6, s, border, ln, map[bool]string{true: "L", false: "R"}[i == 0], false, 0, "")
	}
	pdf.SetXY(tx, y)
	pdf.SetFont(font, "", 9)
	cell(0, "", "B", 0)
	cell(1, "Netto", "B", 0)
	cell(2, "USt.", "B", 0)
	cell(3, "Gesamt", "B", 1)
	if !doc.Kleinunternehmer && len(doc.VATGroups) > 1 {
		for _, g := range doc.VATGroups {
			pdf.SetX(tx)
			cell(0, fmt.Sprintf("USt %d %%", g.Rate), "", 0)
			cell(1, eur(g.NetCents), "", 0)
			cell(2, eur(g.VATCents), "", 0)
			cell(3, eur(g.NetCents+g.VATCents), "", 1)
		}
	}
	vatTotal := "-"
	if !doc.Kleinunternehmer {
		vatTotal = eur(doc.VATCents)
	}
	pdf.SetX(tx)
	pdf.SetFont(font, "B", 9)
	cell(0, "Summen in EUR", "B", 0)
	cell(1, eur(doc.NetCents), "B", 0)
	cell(2, vatTotal, "B", 0)
	cell(3, eur(doc.GrossCents), "B", 1)
	pdf.SetFont(font, "", 9)

	y = max(leftEnd, pdf.GetY()) + 6
	pdf.SetLineWidth(0.5)
	pdf.SetDrawColor(140, 140, 140)
	pdf.Line(marginL, y, marginL+contentW, y)
	pdf.SetLineWidth(0.2)
	pdf.SetDrawColor(0, 0, 0)

	// --- note left, payment right -----------------------------------------
	y += 10
	if y+45 > bottom {
		pdf.AddPage()
		y = pdf.GetY()
	}
	payEnd := y
	if doc.PaymentIBAN != "" {
		payEnd = drawPayment(pdf, doc, 96, y, pageW-marginL-96)
	}
	if note := userNote(doc); note != "" {
		pdf.SetXY(marginL, y)
		pdf.SetFont(font, "", 9)
		pdf.MultiCell(72, 4.5, note, "", "L", false)
	}
	pdf.SetY(max(payEnd, pdf.GetY()))
	return pdf, pdf.Error()
}

// drawHeader names the invoice on every page after the first, so a loose
// page can be traced back to it.
func drawHeader(pdf *fpdf.Fpdf, doc Document, pages int) {
	if doc.Draft {
		drawWatermark(pdf)
	}
	if pdf.PageNo() == 1 {
		return
	}
	pdf.SetFont(font, "", 8)
	pdf.SetTextColor(110, 110, 110)
	pdf.SetXY(marginL, 12)
	pdf.CellFormat(contentW/2, 4, "Rechnung "+doc.Number+" vom "+formatDate(doc.IssueDate), "", 0, "L", false, 0, "")
	page := fmt.Sprintf("Seite %d", pdf.PageNo())
	if pages > 0 {
		page += fmt.Sprintf(" von %d", pages)
	}
	pdf.CellFormat(contentW/2, 4, page, "", 0, "R", false, 0, "")
	pdf.SetDrawColor(210, 210, 210)
	pdf.Line(marginL, 17.5, marginL+contentW, 17.5)
	pdf.SetDrawColor(0, 0, 0)
	pdf.SetTextColor(0, 0, 0)
	pdf.SetXY(marginL, 24)
}

// drawWatermark marks every page of a preview. It is drawn first, so the
// content lies on top of it.
func drawWatermark(pdf *fpdf.Fpdf) {
	pdf.SetAlpha(0.07, "Normal")
	pdf.TransformBegin()
	pdf.TransformRotate(40, pageW/2, 160)
	pdf.SetFont(font, "B", 90)
	pdf.SetXY(0, 145)
	pdf.CellFormat(pageW, 30, "ENTWURF", "", 0, "C", false, 0, "")
	pdf.TransformEnd()
	pdf.SetAlpha(1, "Normal")
}

// drawCarried prints the sums of the lines on earlier pages at the top of a
// continued table.
func drawCarried(pdf *fpdf.Fpdf, w []float64, ku bool, net, vat, gross int64) {
	vatText := "-"
	if !ku {
		vatText = eur(vat)
	}
	y := pdf.GetY() + 1
	pdf.SetFont(font, "B", 9)
	pdf.SetXY(marginL, y)
	pdf.CellFormat(w[0]+w[1]+w[2], 5, "Übertrag", "", 0, "L", false, 0, "")
	pdf.CellFormat(w[3], 5, eur(net), "", 0, "R", false, 0, "")
	pdf.CellFormat(w[4], 5, "", "", 0, "R", false, 0, "")
	pdf.CellFormat(w[5], 5, vatText, "", 0, "R", false, 0, "")
	pdf.CellFormat(w[6], 5, eur(gross), "", 0, "R", false, 0, "")
	pdf.SetFont(font, "", 9)
	pdf.SetDrawColor(200, 200, 200)
	pdf.Line(marginL, y+6, marginL+contentW, y+6)
	pdf.SetDrawColor(0, 0, 0)
	pdf.SetXY(marginL, y+6)
}

// drawPayment draws the transfer request, the account and the payment QR
// code, and returns where it ends.
func drawPayment(pdf *fpdf.Fpdf, doc Document, x, y, w float64) float64 {
	// Fixed lines, not a wrapped paragraph: fpdf can break between an amount
	// and its "€", and this text is short enough to set by hand.
	amount := "von " + eur(doc.GrossCents)
	if doc.DueDate != "" {
		amount += " bis " + formatDate(doc.DueDate)
	}
	pdf.SetFont(font, "B", 9)
	pdf.SetXY(x, y)
	for _, l := range []string{"Bitte überweisen Sie den Gesamtbetrag", amount + " auf", "folgendes Konto:"} {
		pdf.SetX(x)
		pdf.CellFormat(w, 4.5, l, "", 2, "L", false, 0, "")
	}

	const qrSize = 24.0
	payload, err := EPCPayload(doc.Issuer.Name, doc.PaymentIBAN, doc.PaymentBIC, doc.GrossCents, "Rechnung "+doc.Number)
	hasQR := err == nil
	rowsW := w
	if hasQR {
		rowsW = w - qrSize - 3
	}

	top := pdf.GetY() + 2
	rows := [][2]string{{"Kontoname", doc.Issuer.Name}, {"IBAN", groupIBAN(doc.PaymentIBAN)}}
	if doc.PaymentBIC != "" {
		rows = append(rows, [2]string{"BIC", doc.PaymentBIC})
	}
	rows = append(rows, [2]string{"Zweck", doc.Number})
	pdf.SetFont(font, "", 9)
	pdf.SetXY(x, top)
	for _, r := range rows {
		pdf.SetX(x)
		pdf.CellFormat(20, 5, r[0]+":", "", 0, "L", false, 0, "")
		pdf.MultiCell(rowsW-20, 5, r[1], "", "L", false)
	}
	end := pdf.GetY()

	if hasQR && doc.Draft {
		qx := x + w - qrSize
		pdf.SetDrawColor(180, 180, 180)
		pdf.SetDashPattern([]float64{1, 1}, 0)
		pdf.Rect(qx, top, qrSize, qrSize, "D")
		pdf.SetDashPattern(nil, 0)
		pdf.SetDrawColor(0, 0, 0)
		pdf.SetXY(qx, top+qrSize/2-4)
		pdf.SetFont(font, "", 7)
		pdf.SetTextColor(110, 110, 110)
		pdf.MultiCell(qrSize, 3.5, "QR-Code nach dem Ausstellen", "", "C", false)
		pdf.SetTextColor(0, 0, 0)
		end = max(end, top+qrSize+4)
	} else if hasQR {
		qx := x + w - qrSize
		if drawQR(pdf, payload, qx, top, qrSize) {
			pdf.SetXY(qx-4, top+qrSize+0.5)
			pdf.SetFont(font, "", 6.5)
			pdf.SetTextColor(110, 110, 110)
			pdf.CellFormat(qrSize+8, 3, "Mit der Banking-App scannen", "", 0, "C", false, 0, "")
			pdf.SetTextColor(0, 0, 0)
			end = max(end, top+qrSize+4)
		}
	}
	return end
}

// drawQR draws the code as vector squares, so it prints sharply at any size.
// The EPC guidelines ask for error correction level M.
func drawQR(pdf *fpdf.Fpdf, payload string, x, y, size float64) bool {
	code, err := qr.Encode(payload, qr.M)
	if err != nil {
		return false
	}
	m := size / float64(code.Size)
	pdf.SetFillColor(0, 0, 0)
	for row := 0; row < code.Size; row++ {
		// Runs of dark modules become one rectangle: fewer objects, and no
		// hairline gaps between neighbours in some viewers.
		for col := 0; col < code.Size; {
			if !code.Black(col, row) {
				col++
				continue
			}
			start := col
			for col < code.Size && code.Black(col, row) {
				col++
			}
			pdf.Rect(x+float64(start)*m, y+float64(row)*m, float64(col-start)*m, m+0.01, "F")
		}
	}
	return true
}

// drawFooter sets the issuer's standing details in three columns under a
// hairline: who, how to reach them, where to pay.
func drawFooter(pdf *fpdf.Fpdf, doc Document) {
	top := 297 - footerH
	pdf.SetDrawColor(210, 210, 210)
	pdf.Line(marginL, top, marginL+contentW, top)
	pdf.SetDrawColor(0, 0, 0)

	// The street keeps its own line; postcode, town and country share one.
	addr := splitLines(doc.Issuer.Address)
	left := []string{doc.Issuer.Name}
	if len(addr) > 0 {
		left = append(left, addr[0])
	}
	if len(addr) > 1 {
		left = append(left, strings.Join(addr[1:], " · "))
	}
	if doc.Issuer.UID != "" {
		left = append(left, "UID "+doc.Issuer.UID)
	}
	var right []string
	if doc.PaymentIBAN != "" {
		right = []string{doc.Issuer.Name, "IBAN " + groupIBAN(doc.PaymentIBAN)}
		if doc.PaymentBIC != "" {
			right = append(right, "BIC "+doc.PaymentBIC)
		}
	}

	colW := contentW / 3
	for i, col := range [][]string{left, splitLines(doc.Contact), right} {
		x := marginL + float64(i)*colW
		pdf.SetXY(x, top+3)
		for n, l := range col {
			// The first line of the outer columns is its heading: the name.
			if n == 0 && i != 1 {
				pdf.SetFont(font, "B", 7.5)
				pdf.SetTextColor(80, 80, 80)
			} else {
				pdf.SetFont(font, "", 7.5)
				pdf.SetTextColor(115, 115, 115)
			}
			pdf.SetX(x)
			pdf.CellFormat(colW, 3.6, l, "", 2, []string{"L", "C", "R"}[i], false, 0, "")
		}
	}
	pdf.SetTextColor(0, 0, 0)
}

func party(pdf *fpdf.Fpdf, x, w float64, p Party) {
	lines := append([]string{p.Name}, splitLines(p.Address)...)
	if p.UID != "" {
		lines = append(lines, "UID: "+p.UID)
	}
	for _, l := range lines {
		pdf.SetX(x)
		pdf.MultiCell(w, 5, l, "", "L", false)
	}
}

// userNote is the note as the user wrote it. Build puts the exemption text in
// front of it, and the PDF prints that beside the totals instead.
func userNote(doc Document) string {
	note := doc.Note
	if doc.Kleinunternehmer {
		note = strings.TrimPrefix(note, KleinunternehmerNote)
	}
	return strings.TrimSpace(note)
}

// fit scales w×h down to fit in maxW×maxH, keeping the aspect ratio.
func fit(w, h, maxW, maxH float64) (float64, float64) {
	if w <= 0 || h <= 0 {
		return maxW, maxH
	}
	s := min(maxW/w, maxH/h)
	return w * s, h * s
}

func eur(cents int64) string { return money.Format(cents) + " €" }

// drawTableHeader draws the line-item header row. It is a function because a
// multi-page invoice needs it again at the top of each page.
func drawTableHeader(pdf *fpdf.Fpdf, w []float64, headers, aligns []string) {
	pdf.SetFont(font, "", 9)
	for i, h := range headers {
		pdf.CellFormat(w[i], 7, h, "B", 0, aligns[i], false, 0, "")
	}
	pdf.Ln(-1)
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// formatDate turns '2026-08-18' into '18.08.2026' for display.
func formatDate(iso string) string {
	if len(iso) != 10 {
		return iso
	}
	return iso[8:10] + "." + iso[5:7] + "." + iso[0:4]
}
