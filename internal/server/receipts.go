package server

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/chrthal/accountant/internal/money"
	"github.com/chrthal/accountant/internal/ocr"
	"github.com/chrthal/accountant/internal/store"
)

// A receipt photo or a supplier's PDF invoice becomes a *draft* booking:
// recognised values land in the ordinary entry form, editable, with the raw
// text beside it so a wrong number is obvious. Nothing is saved until the user
// submits (ADR 0002). The file itself is kept as the Beleg (ADR 0016).

func (s *Server) handleReceiptForm(w http.ResponseWriter, r *http.Request) {
	waiting, err := s.store.ImportQueue(r.Context(), store.ReceiptQueue)
	if err != nil {
		s.fail(w, "receipt queue", err)
		return
	}
	s.render(w, http.StatusOK, "receipt_form.html", pageData{
		OCRAvailable:  ocr.Available(),
		PDFAvailable:  ocr.PDFAvailable(),
		QueuedImports: len(waiting),
	})
}

func (s *Server) handleReceiptUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(ocr.MaxImageSize); err != nil {
		s.receiptError(w, s.T("Die Datei konnte nicht gelesen werden (maximal 10 MiB)."))
		return
	}
	file, header, err := r.FormFile("photo")
	if err != nil {
		s.receiptError(w, s.T("Bitte ein Foto oder eine PDF-Rechnung auswählen."))
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, ocr.MaxImageSize+1))
	if err != nil {
		s.receiptError(w, s.T("Die Datei konnte nicht gelesen werden."))
		return
	}
	// Refuse what could not be kept as a Beleg anyway, before spending a
	// subprocess on it.
	if _, err := store.NewAttachment(header.Filename, data); err != nil {
		s.receiptError(w, s.Tf("Diese Datei kann nicht als Beleg verwendet werden: %s", s.errText(err)))
		return
	}

	settings, err := s.store.Settings(r.Context())
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	draft, method, note := s.readReceipt(r.Context(), data, header.Filename, settings)
	s.renderPrefilledEntry(w, r, draft, note, data, header.Filename, method)
}

// readReceipt reads a Beleg: what it found, how, and what to tell the user
// when it found nothing. A Beleg that cannot be read is still a Beleg; the
// user types the figures.
func (s *Server) readReceipt(ctx context.Context, data []byte, filename string, settings store.Settings) (ocr.Draft, string, string) {
	text, method, err := ocr.Read(ctx, data, filename)
	switch {
	case errors.Is(err, ocr.ErrNoReader):
		return ocr.Draft{}, "", s.T("Auf diesem Server ist keine Texterkennung für diesen Dateityp installiert. Der Beleg wird trotzdem mit der Buchung abgelegt, bitte die Werte eintragen.")
	case err != nil:
		log.Printf("read receipt: %v", err)
		return ocr.Draft{}, "", s.Tf("Die Texterkennung hat nichts gefunden (%s). Bitte die Werte eintragen.", s.errText(err))
	}
	// The user's own business is printed on an incoming invoice as the
	// recipient, so it must not be mistaken for the vendor.
	draft := ocr.Extract(text, settings.IssuerName)
	if !draft.Found() {
		return draft, method, s.T("Im Beleg war nichts Verwertbares zu erkennen. Bitte die Werte eintragen.")
	}
	return draft, method, ""
}

// renderPrefilledEntry opens the normal booking form with whatever was read.
// Fields that could not be read stay empty rather than guessed at (ADR 0011).
// The photo itself travels with the form and is stored as the booking's Beleg
// on save (ADR 0016). Nothing is written before the user confirms.
func (s *Server) renderPrefilledEntry(w http.ResponseWriter, r *http.Request, draft ocr.Draft, note string,
	image []byte, filename, method string) {

	ctx := r.Context()
	rate := 20
	settings, err := s.store.Settings(ctx)
	if err == nil {
		rate = settings.OutgoingVAT()
	}
	form := map[string]string{
		"direction": "outgoing",
		"booked_on": draft.BookedOn,
		"vat_rate":  strconv.Itoa(rate),
	}
	// The same proposal as the bulk upload: a similar earlier booking, else
	// the category keywords (ADR 0044, ADR 0021).
	if hist, err := s.loadHistory(ctx); err == nil {
		cats, _ := s.store.ListCategories(ctx)
		contacts, _ := s.store.ListContacts(ctx)
		p := s.proposeReceipt(ctx, hist, draft, "", store.Attachment{}, settings, cats, contacts)
		for _, k := range []string{"category_id", "contact_id", "vat_rate", "business_pct", "business_note"} {
			if p[k] != "" {
				form[k] = p[k]
			}
		}
		if p["why"] != "" {
			form["prefill_note"] = s.Tf("Vorausgefüllt %s.", p["why"])
		}
	}
	if form["booked_on"] == "" {
		form["booked_on"] = time.Now().Format("2006-01-02")
	}
	if draft.AmountCents > 0 {
		form["amount"] = money.Format(draft.AmountCents)
	}
	if desc := draft.Description(); desc != "" {
		form["description"] = truncate(desc, 200)
	}
	form["read_method"] = method
	if draft.VATRateRead {
		form["vat_rate"] = strconv.Itoa(draft.VATRate)
	}
	// Only carry a photo that would be accepted as a Beleg; anything else is
	// dropped here rather than failing the booking later.
	if a, err := store.NewAttachment(filename, image); err == nil {
		form["receipt_payload"] = base64.StdEncoding.EncodeToString(image)
		form["receipt_name"] = a.Filename
		if form["receipt_name"] == "" {
			form["receipt_name"] = "Beleg"
		}
	}
	s.renderEntryForm(w, r, http.StatusOK, form, note, &draft)
}

func (s *Server) receiptError(w http.ResponseWriter, msg string) {
	s.render(w, http.StatusUnprocessableEntity, "receipt_form.html", pageData{
		OCRAvailable: ocr.Available(), PDFAvailable: ocr.PDFAvailable(), Error: msg,
	})
}
