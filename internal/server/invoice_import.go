package server

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/chrthal/accountant/internal/match"
	"github.com/chrthal/accountant/internal/money"
	"github.com/chrthal/accountant/internal/ocr"
	"github.com/chrthal/accountant/internal/store"
)

// Importing invoices written with another tool (ADR 0031): the original PDF
// is read like a Beleg, the invoice form opens pre-filled, and on save the
// invoice is stored as issued, with the PDF as its document.

func (s *Server) handleInvoiceImportForm(w http.ResponseWriter, r *http.Request) {
	s.renderInvoiceImport(w, r, http.StatusOK, "")
}

func (s *Server) invoiceImportError(w http.ResponseWriter, r *http.Request, msg string) {
	s.renderInvoiceImport(w, r, http.StatusUnprocessableEntity, msg)
}

func (s *Server) renderInvoiceImport(w http.ResponseWriter, r *http.Request, status int, msg string) {
	queued, err := s.store.ImportQueue(r.Context(), store.InvoiceQueue)
	if err != nil {
		s.fail(w, "import queue", err)
		return
	}
	s.render(w, status, "invoice_import.html", pageData{PDFAvailable: ocr.PDFAvailable(), Error: msg, QueuedImports: len(queued)})
}

// handleInvoiceImportUpload reads the original and opens the form with what
// it found. Nothing is stored yet.
func (s *Server) handleInvoiceImportUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(store.MaxAttachmentSize); err != nil {
		s.invoiceImportError(w, r, s.T("Die Datei konnte nicht gelesen werden (maximal 10 MiB)."))
		return
	}
	file, header, err := r.FormFile("original")
	if err != nil {
		s.invoiceImportError(w, r, s.T("Bitte die PDF-Datei der Rechnung auswählen."))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, store.MaxAttachmentSize+1))
	if err != nil {
		s.invoiceImportError(w, r, s.T("Die Datei konnte nicht gelesen werden."))
		return
	}
	original, err := store.NewAttachment(header.Filename, data)
	if err != nil || original.ContentType != "application/pdf" {
		s.invoiceImportError(w, r, s.T("Importiert werden Rechnungen als PDF-Datei."))
		return
	}

	settings, err := s.store.Settings(r.Context())
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	contacts, err := s.store.ListContacts(r.Context())
	if err != nil {
		s.fail(w, "list contacts", err)
		return
	}
	read := s.readInvoicePDF(r.Context(), data, header.Filename, settings, contacts)
	form := read.Fields
	form["import"], form["original"], form["original_name"] = "1", base64.StdEncoding.EncodeToString(data), original.Filename
	s.renderInvoiceForm(w, r, http.StatusOK, form, read.lines(s), read.Note)
}

// readInvoice is what an invoice PDF yielded for the import form.
type readInvoice struct {
	Fields     map[string]string // number, issue_date, service_date, contact_id, new_contact_name, read_total
	GrossCents int64
	VATRate    int
	Note       string // why the user has to type, if they do
}

// lines is the one position the import starts with: the total, its net
// following from the rate. The user splits it if the positions matter.
func (ri readInvoice) lines(s *Server) []lineForm {
	if ri.GrossCents <= 0 {
		return nil
	}
	_, net := money.SplitVAT(ri.GrossCents, ri.VATRate)
	return []lineForm{{Description: s.T("Leistung laut Rechnung"), Quantity: "1",
		UnitPrice: money.Format(net), VATRate: strconv.Itoa(ri.VATRate)}}
}

// readInvoicePDF reads an invoice PDF for import: number, date, total, rate
// and the recipient, matched to an existing contact where the name fits.
func (s *Server) readInvoicePDF(ctx context.Context, data []byte, filename string, settings store.Settings, contacts []store.Contact) readInvoice {
	ri := readInvoice{Fields: map[string]string{}}
	text, _, err := ocr.Read(ctx, data, filename)
	if err != nil {
		if !errors.Is(err, ocr.ErrNoReader) {
			log.Printf("read invoice for import: %v", err)
		}
		ri.Note = s.T("Aus der PDF-Datei war nichts zu lesen. Bitte die Angaben von der Rechnung abtippen.")
		return ri
	}
	// The user's own business is the sender here; the first other name is
	// usually the recipient.
	d := ocr.Extract(text, settings.IssuerName)
	ri.Fields["number"] = d.InvoiceNumber
	ri.Fields["issue_date"], ri.Fields["service_date"] = d.BookedOn, d.BookedOn
	if d.Vendor != "" {
		ri.Fields["new_contact_name"] = d.Vendor
		for _, c := range contacts {
			if match.SimilarName(d.Vendor, c.Name) {
				ri.Fields["contact_id"], ri.Fields["new_contact_name"] = strconv.FormatInt(c.ID, 10), ""
				break
			}
		}
	}
	if d.AmountCents > 0 {
		ri.Fields["read_total"] = money.Format(d.AmountCents)
		ri.GrossCents, ri.VATRate = d.AmountCents, 20
		if settings.KUOn(d.BookedOn) {
			ri.VATRate = 0
		} else if d.VATRateRead {
			ri.VATRate = d.VATRate
		}
	}
	if !d.Found() && d.InvoiceNumber == "" {
		ri.Note = s.T("In der PDF-Datei war nichts Verwertbares zu erkennen. Bitte die Angaben von der Rechnung abtippen.")
	}
	return ri
}

// handleInvoiceImportSave stores the confirmed import.
func (s *Server) handleInvoiceImportSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2*store.MaxAttachmentSize)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	form, lines := s.readInvoiceForm(r)
	for _, k := range []string{"import", "original", "original_name", "number", "new_contact_name", "read_total", "queue_id"} {
		form[k] = r.FormValue(k)
	}
	form["import"] = "1"

	// From the bulk review, the original stays on the waiting list instead
	// of travelling through the page (ADR 0032).
	queueID, _ := strconv.ParseInt(form["queue_id"], 10, 64)
	data, err := base64.StdEncoding.DecodeString(form["original"])
	if queueID != 0 {
		var q store.QueuedImport
		q, data, err = s.store.QueuedOriginal(r.Context(), store.InvoiceQueue, queueID)
		form["original_name"] = q.Filename
	}
	original, err2 := store.NewAttachment(form["original_name"], data)
	if err != nil || err2 != nil {
		s.invoiceImportError(w, r, s.T("Die PDF-Datei ist beim Absenden verloren gegangen. Bitte noch einmal hochladen."))
		return
	}

	// A recipient typed by name becomes a new customer, so a migration does
	// not need a detour through the contacts page for every old customer.
	if name := strings.TrimSpace(form["new_contact_name"]); form["contact_id"] == "" && name != "" {
		id, err := s.store.CreateContact(r.Context(), store.Contact{Kind: "customer", Name: name})
		if err != nil {
			s.renderInvoiceForm(w, r, http.StatusUnprocessableEntity, form, lines, s.errText(err))
			return
		}
		form["contact_id"], form["new_contact_name"] = strconv.FormatInt(id, 10), ""
	}

	in, msg := s.parseInvoiceForm(form, lines)
	if msg != "" {
		s.renderInvoiceForm(w, r, http.StatusUnprocessableEntity, form, lines, msg)
		return
	}
	inv, err := s.store.ImportInvoice(r.Context(), store.ImportedInvoice{NewInvoice: in, Number: form["number"], Original: original, QueueID: queueID})
	if err != nil {
		s.renderInvoiceForm(w, r, http.StatusUnprocessableEntity, form, lines, s.errText(err))
		return
	}
	http.Redirect(w, r, "/invoices/"+strconv.FormatInt(inv.ID, 10)+"?imported=1", http.StatusSeeOther)
}
