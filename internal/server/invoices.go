package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chrthal/accountant/internal/invoice"
	"github.com/chrthal/accountant/internal/money"
	"github.com/chrthal/accountant/internal/store"
)

// lineForm is one invoice line as the form holds it. It stays text, so a
// rejected submission can hand back exactly what was typed.
type lineForm struct {
	Description string
	Quantity    string
	Unit        string
	UnitPrice   string
	VATRate     string
}

// lineCtx is what the "invoice-line" template needs. Index -1 renders the
// placeholder row for the browser-side <template>, with markers that app.js
// replaces with the next free index.
type lineCtx struct {
	Index  string
	Number string
	Line   lineForm
	KU     bool
}

func newLineCtx(i int, l lineForm, ku bool) lineCtx {
	if i < 0 {
		return lineCtx{Index: "__i__", Number: "__n__", Line: l, KU: ku}
	}
	return lineCtx{Index: strconv.Itoa(i), Number: strconv.Itoa(i + 1), Line: l, KU: ku}
}

const (
	// blankInvoiceLines is how many empty rows a new invoice form offers. With
	// script, "Position hinzufügen" adds more. Without it, this is the limit.
	blankInvoiceLines = 3
	// maxInvoiceLines caps how many indexed rows the server reads back, so a
	// crafted form cannot make it loop without bound.
	maxInvoiceLines = 100
)

func (s *Server) handleInvoices(w http.ResponseWriter, r *http.Request) {
	invoices, err := s.store.ListInvoices(r.Context(), 200)
	if err != nil {
		s.fail(w, "list invoices", err)
		return
	}
	var open, overdue int64
	for _, inv := range invoices {
		if !inv.IsPaid() {
			open += inv.OpenCents()
			if inv.IsOverdue() {
				overdue += inv.OpenCents()
			}
		}
	}
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	drafts, err := s.draftRows(r.Context(), settings.Kleinunternehmer)
	if err != nil {
		s.fail(w, "list drafts", err)
		return
	}
	notice := map[string]string{
		"saved":   s.T("Entwurf gespeichert. Er bekommt erst beim Ausstellen eine Nummer."),
		"deleted": s.T("Entwurf gelöscht."),
		"gone":    s.T("Der Entwurf wurde inzwischen gelöscht oder ausgestellt."),
	}[r.URL.Query().Get("draft")]
	if n := r.URL.Query().Get("imported_bulk"); n != "" {
		notice = s.Tf("%s Rechnung(en) importiert.", n)
	}
	queued, err := s.store.ImportQueue(r.Context(), store.InvoiceQueue)
	if err != nil {
		s.fail(w, "import queue", err)
		return
	}
	due, err := s.dueRecurring(r, settings.Kleinunternehmer)
	if err != nil {
		s.fail(w, "due recurring", err)
		return
	}
	s.render(w, http.StatusOK, "invoices.html", pageData{
		Invoices: invoices, OpenCents: open, OverdueCents: overdue, InvoiceDrafts: drafts, Notice: notice,
		QueuedImports: len(queued), DueRecurring: due,
	})
}

func (s *Server) handleInvoiceForm(w http.ResponseWriter, r *http.Request) {
	today := time.Now()
	form := map[string]string{
		"issue_date":   today.Format("2006-01-02"),
		"service_date": today.Format("2006-01-02"),
	}
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	if settings.PaymentTermsDays > 0 {
		form["due_date"] = today.AddDate(0, 0, settings.PaymentTermsDays).Format("2006-01-02")
	}

	// "Als Vorlage verwenden": recipient, positions and note of an earlier
	// invoice, with fresh dates. Nothing of the old invoice changes.
	var lines []lineForm
	if from, err := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64); err == nil {
		src, err := s.store.GetInvoice(r.Context(), from)
		if err == nil {
			form["contact_id"] = strconv.FormatInt(src.ContactID, 10)
			form["note"] = strings.TrimSpace(strings.TrimPrefix(src.Note, invoice.KleinunternehmerNote))
			for _, l := range src.Lines {
				lines = append(lines, lineForm{
					Description: l.Description, Quantity: money.FormatQuantity(l.QuantityMilli),
					Unit: l.Unit, UnitPrice: money.Format(l.UnitPriceCents), VATRate: strconv.Itoa(l.VATRate),
				})
			}
		}
	}
	if rl, note, err := s.recurringInvoiceForm(r, form); err != nil {
		s.fail(w, "recurring invoice", err)
		return
	} else if rl != nil {
		lines, form["recurring_note"] = rl, note
	}
	s.renderInvoiceForm(w, r, http.StatusOK, form, lines, "")
}

func (s *Server) renderInvoiceForm(w http.ResponseWriter, r *http.Request, status int,
	form map[string]string, lines []lineForm, errMsg string) {

	contacts, err := s.store.ListContacts(r.Context())
	if err != nil {
		s.fail(w, "list contacts", err)
		return
	}
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	for len(lines) < blankInvoiceLines {
		lines = append(lines, lineForm{})
	}
	defaultRate := "20"
	if settings.Kleinunternehmer {
		defaultRate = "0"
	}
	for i := range lines {
		if lines[i].VATRate == "" {
			lines[i].VATRate = defaultRate
		}
	}
	form["default_rate"] = defaultRate
	s.render(w, status, "invoice_form.html", pageData{
		Contacts: contacts, Settings: settings, Form: form, Lines: lines, Error: errMsg,
	})
}

// handleInvoiceCreate serves the three buttons of the invoice form: save as
// draft, preview as PDF, and issue (ADR 0026).
func (s *Server) handleInvoiceCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	form, lines := s.readInvoiceForm(r)
	draftID, _ := strconv.ParseInt(form["draft_id"], 10, 64)

	if r.FormValue("action") == "draft" {
		// A draft is kept as typed: nothing is checked, so half-finished
		// input can be saved and finished later.
		dl := make([]store.DraftLine, 0, len(lines))
		for _, l := range lines {
			if strings.TrimSpace(l.Description) != "" || strings.TrimSpace(l.UnitPrice) != "" {
				dl = append(dl, store.DraftLine(l))
			}
		}
		fields := map[string]string{}
		for _, k := range []string{"contact_id", "issue_date", "service_date", "due_date", "note", "recurring_id"} {
			fields[k] = form[k]
		}
		if _, err := s.store.SaveDraft(r.Context(), draftID, fields, dl); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				s.renderInvoiceForm(w, r, http.StatusUnprocessableEntity, form, lines,
					s.T("Der Entwurf wurde inzwischen gelöscht oder ausgestellt."))
				return
			}
			s.fail(w, "save draft", err)
			return
		}
		http.Redirect(w, r, "/invoices?draft=saved", http.StatusSeeOther)
		return
	}

	in, msg := s.parseInvoiceForm(form, lines)
	if msg != "" {
		s.renderInvoiceForm(w, r, http.StatusUnprocessableEntity, form, lines, msg)
		return
	}

	if r.FormValue("action") == "preview" {
		doc, err := s.store.PreviewInvoice(r.Context(), in)
		if err != nil {
			s.renderInvoiceForm(w, r, http.StatusUnprocessableEntity, form, lines, s.errText(err))
			return
		}
		logo, err := s.invoiceLogo(r.Context())
		if err != nil {
			s.fail(w, "read logo", err)
			return
		}
		out, err := invoice.RenderPDF(doc, logo)
		if err != nil {
			s.fail(w, "render preview", err)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `inline; filename="Rechnungsentwurf.pdf"`)
		w.Write(out)
		return
	}

	in.DraftID = draftID
	inv, err := s.store.CreateInvoice(r.Context(), in)
	if err != nil {
		s.renderInvoiceForm(w, r, http.StatusUnprocessableEntity, form, lines, s.errText(err))
		return
	}
	http.Redirect(w, r, "/invoices/"+strconv.FormatInt(inv.ID, 10), http.StatusSeeOther)
}

// readInvoiceForm reads the invoice form as typed. Rows are indexed; every
// index present is read, so rows added in the browser just continue the
// numbering.
func (s *Server) readInvoiceForm(r *http.Request) (map[string]string, []lineForm) {
	form := map[string]string{
		"draft_id":     r.FormValue("draft_id"),
		"recurring_id": r.FormValue("recurring_id"),
		"contact_id":   r.FormValue("contact_id"),
		"issue_date":   s.parseDate(r.FormValue("issue_date")),
		"service_date": s.parseDate(r.FormValue("service_date")),
		"due_date":     s.parseDate(r.FormValue("due_date")),
		"note":         r.FormValue("note"),
	}
	var lines []lineForm
	for i := range maxInvoiceLines {
		idx := strconv.Itoa(i)
		if _, ok := r.Form["description"+idx]; !ok {
			continue
		}
		lines = append(lines, lineForm{
			Description: r.FormValue("description" + idx),
			Quantity:    r.FormValue("quantity" + idx),
			Unit:        r.FormValue("unit" + idx),
			UnitPrice:   r.FormValue("unit_price" + idx),
			VATRate:     r.FormValue("vat_rate" + idx),
		})
	}
	return form, lines
}

// parseInvoiceForm turns the typed form into an invoice to build, or returns
// the message to show. Empty rows are ignored, so a spare blank row never
// blocks a short invoice.
func (s *Server) parseInvoiceForm(form map[string]string, lines []lineForm) (store.NewInvoice, string) {
	var newLines []store.InvoiceLine
	for i, lf := range lines {
		if strings.TrimSpace(lf.Description) == "" && strings.TrimSpace(lf.UnitPrice) == "" {
			continue
		}
		pos := strconv.Itoa(i + 1)
		qty, err := money.ParseQuantity(lf.Quantity)
		if err != nil {
			return store.NewInvoice{}, s.Tf("Position %s: Menge ist keine gültige Zahl (z. B. 1,5)", pos)
		}
		price, err := money.Parse(lf.UnitPrice)
		if err != nil {
			return store.NewInvoice{}, s.Tf("Position %s: Einzelpreis ist keine gültige Zahl (z. B. 1.234,56)", pos)
		}
		rate, err := strconv.Atoi(lf.VATRate)
		if err != nil {
			return store.NewInvoice{}, s.Tf("Position %s: USt-Satz ist ungültig", pos)
		}
		newLines = append(newLines, store.InvoiceLine{
			Description: lf.Description, QuantityMilli: qty, Unit: lf.Unit,
			UnitPriceCents: price, VATRate: rate,
		})
	}
	contactID, err := strconv.ParseInt(form["contact_id"], 10, 64)
	if err != nil || contactID <= 0 {
		return store.NewInvoice{}, s.T("Bitte einen Rechnungsempfänger auswählen")
	}
	recurringID, _ := strconv.ParseInt(form["recurring_id"], 10, 64)
	return store.NewInvoice{
		RecurringID: recurringID,
		ContactID:   contactID,
		IssueDate:   form["issue_date"],
		ServiceDate: form["service_date"],
		DueDate:     strings.TrimSpace(form["due_date"]),
		Note:        form["note"],
		Lines:       newLines,
	}, ""
}

// invoiceDraftRow is a draft as the invoice list shows it.
type invoiceDraftRow struct {
	ID         int64
	Recipient  string
	IssueDate  string
	Positions  int
	GrossCents int64
	UpdatedAt  time.Time
}

// draftRows summarises the drafts for the list. The total is what the typed
// lines add up to; a line that does not parse yet counts as nothing.
func (s *Server) draftRows(ctx context.Context, ku bool) ([]invoiceDraftRow, error) {
	drafts, err := s.store.ListDrafts(ctx)
	if err != nil || len(drafts) == 0 {
		return nil, err
	}
	contacts, err := s.store.ListContacts(ctx)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, c := range contacts {
		names[strconv.FormatInt(c.ID, 10)] = c.Name
	}
	var out []invoiceDraftRow
	for _, d := range drafts {
		row := invoiceDraftRow{ID: d.ID, Recipient: names[d.Fields["contact_id"]], IssueDate: d.Fields["issue_date"],
			Positions: len(d.Lines), UpdatedAt: d.UpdatedAt}
		for _, l := range d.Lines {
			qty, err1 := money.ParseQuantity(l.Quantity)
			price, err2 := money.Parse(l.UnitPrice)
			rate, err3 := strconv.Atoi(l.VATRate)
			if err1 != nil || err2 != nil || err3 != nil {
				continue
			}
			net := money.LineNet(price, qty)
			row.GrossCents += net
			if !ku {
				row.GrossCents += money.VATFromNet(net, rate)
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *Server) handleDraftEdit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad draft id", http.StatusBadRequest)
		return
	}
	d, err := s.store.GetDraft(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.Redirect(w, r, "/invoices?draft=gone", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.fail(w, "get draft", err)
		return
	}
	form := map[string]string{"draft_id": strconv.FormatInt(d.ID, 10)}
	for k, v := range d.Fields {
		form[k] = v
	}
	lines := make([]lineForm, 0, len(d.Lines))
	for _, l := range d.Lines {
		lines = append(lines, lineForm(l))
	}
	s.renderInvoiceForm(w, r, http.StatusOK, form, lines, "")
}

func (s *Server) handleDraftDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad draft id", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteDraft(r.Context(), id); err != nil {
		s.fail(w, "delete draft", err)
		return
	}
	http.Redirect(w, r, "/invoices?draft=deleted", http.StatusSeeOther)
}

func (s *Server) invoiceFromPath(w http.ResponseWriter, r *http.Request) (store.Invoice, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad invoice id", http.StatusBadRequest)
		return store.Invoice{}, false
	}
	inv, err := s.store.GetInvoice(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "invoice not found", http.StatusNotFound)
		return store.Invoice{}, false
	}
	if err != nil {
		s.fail(w, "get invoice", err)
		return store.Invoice{}, false
	}
	return inv, true
}

func (s *Server) handleInvoice(w http.ResponseWriter, r *http.Request) {
	s.renderInvoice(w, r, http.StatusOK, "")
}

func (s *Server) renderInvoice(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
	inv, ok := s.invoiceFromPath(w, r)
	if !ok {
		return
	}
	payments, err := s.store.EntriesForInvoice(r.Context(), inv.ID)
	if err != nil {
		s.fail(w, "invoice payments", err)
		return
	}
	categories, err := s.store.ListCategories(r.Context())
	if err != nil {
		s.fail(w, "list categories", err)
		return
	}
	doc := inv.Document()
	var notice string
	switch {
	case r.URL.Query().Get("paid") == "1":
		notice = s.T("Zahlung gebucht. Die Einnahme steht jetzt im Journal.")
	case r.URL.Query().Get("imported") == "1":
		notice = s.T("Rechnung importiert. Die Original-PDF ist ihr Dokument; ist sie schon bezahlt, die Zahlung unten buchen oder über die Bank zuordnen.")
	}
	s.render(w, status, "invoice.html", pageData{
		Invoice: &inv, Entries: payments, Categories: categories, Error: errMsg, Notice: notice,
		VATGroups: doc.VATGroups,
		Form:      map[string]string{"paid_on": time.Now().Format("2006-01-02")},
	})
}

func (s *Server) handleInvoicePayment(w http.ResponseWriter, r *http.Request) {
	inv, ok := s.invoiceFromPath(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	p := store.InvoicePayment{InvoiceID: inv.ID, PaidOn: s.parseDate(r.FormValue("paid_on"))}
	if v := strings.TrimSpace(r.FormValue("amount")); v != "" {
		amount, err := money.Parse(v)
		if err != nil {
			s.renderInvoice(w, r, http.StatusUnprocessableEntity, s.T("Betrag ist keine gültige Zahl (z. B. 1.234,56)"))
			return
		}
		p.AmountCents = amount
	}
	if id, err := strconv.ParseInt(r.FormValue("category_id"), 10, 64); err == nil && id > 0 {
		p.CategoryID = &id
	}
	_, err := s.store.BookInvoicePayment(r.Context(), p)
	switch {
	case errors.Is(err, store.ErrAlreadyPaid):
		s.renderInvoice(w, r, http.StatusConflict, s.T("Diese Rechnung ist bereits bezahlt."))
	case errors.Is(err, store.ErrOverpaid):
		s.renderInvoice(w, r, http.StatusUnprocessableEntity, s.Tf("Die Zahlung ist höher als der offene Betrag von %s €.", s.eur(inv.OpenCents())))
	case err != nil:
		s.renderInvoice(w, r, http.StatusUnprocessableEntity, s.errText(err))
	default:
		http.Redirect(w, r, "/invoices/"+strconv.FormatInt(inv.ID, 10)+"?paid=1", http.StatusSeeOther)
	}
}

func (s *Server) handleInvoicePDF(w http.ResponseWriter, r *http.Request) {
	inv, ok := s.invoiceFromPath(w, r)
	if !ok {
		return
	}
	if inv.Imported {
		o, err := s.store.InvoiceOriginal(r.Context(), inv.ID)
		if err != nil {
			s.fail(w, "invoice original", err)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `inline; filename="Rechnung-`+inv.Number+`.pdf"`)
		w.Write(o.Data)
		return
	}
	logo, err := s.invoiceLogo(r.Context())
	if err != nil {
		s.fail(w, "read logo", err)
		return
	}
	out, err := invoice.RenderPDF(inv.Document(), logo)
	if err != nil {
		log.Printf("render invoice %d: %v", inv.ID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="Rechnung-`+inv.Number+`.pdf"`)
	w.Write(out)
}

// invoiceLogo is the stored logo in the form the PDF writer takes, or nil.
func (s *Server) invoiceLogo(ctx context.Context) (*invoice.Logo, error) {
	l, ok, err := s.store.GetLogo(ctx)
	if err != nil || !ok {
		return nil, err
	}
	return &invoice.Logo{Data: l.Data, Type: logoType(l.ContentType)}, nil
}

func logoType(contentType string) string {
	if contentType == "image/jpeg" {
		return "JPG"
	}
	return "PNG"
}
