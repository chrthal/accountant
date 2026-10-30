package server

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/chrthal/accountant/internal/money"
	"github.com/chrthal/accountant/internal/store"
)

// Bulk import of invoices from another tool (ADR 0032). The PDFs go onto a
// waiting list, each read like a single import (ADR 0031); the user checks
// them in one list and imports the ticked ones. Every row is stored exactly
// as a single import would store it.

// queueRow is one waiting invoice as the review shows it.
type queueRow struct {
	store.QueuedImport
	Problem string // why it is not ticked
	Err     string // why importing it failed
	Picked  bool
	Pending bool // a Beleg not read yet (ADR 0042)
}

// handleInvoiceImportBatch reads the uploaded PDFs onto the waiting list. The
// files are read one at a time from the request, so a large batch never sits
// in memory as a whole.
func (s *Server) handleInvoiceImportBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	queued, err := s.store.ImportQueue(ctx, store.InvoiceQueue)
	if err != nil {
		s.fail(w, "import queue", err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(store.MaxQueuedImports)*(store.MaxAttachmentSize+1<<16))
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	settings, err := s.store.Settings(ctx)
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	contacts, err := s.store.ListContacts(ctx)
	if err != nil {
		s.fail(w, "list contacts", err)
		return
	}

	added, room := 0, store.MaxQueuedImports-len(queued)
	var skipped []string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.invoiceImportError(w, r, s.T("Die Dateien konnten nicht vollständig hochgeladen werden."))
			return
		}
		if part.FormName() != "originals" || part.FileName() == "" {
			part.Close()
			continue
		}
		name := part.FileName()
		data, err := io.ReadAll(io.LimitReader(part, store.MaxAttachmentSize+1))
		part.Close()
		if err != nil {
			s.invoiceImportError(w, r, s.T("Die Dateien konnten nicht vollständig hochgeladen werden."))
			return
		}
		original, err := store.NewAttachment(name, data)
		if err != nil || original.ContentType != "application/pdf" {
			skipped = append(skipped, name)
			continue
		}
		if added >= room {
			skipped = append(skipped, name)
			continue
		}
		read := s.readInvoicePDF(ctx, data, name, settings, contacts)
		f := map[string]string{
			"number": read.Fields["number"], "issue_date": read.Fields["issue_date"],
			"contact_id": read.Fields["contact_id"], "new_contact_name": read.Fields["new_contact_name"],
			"vat_rate": strconv.Itoa(read.VATRate), "note": read.Note,
		}
		if read.GrossCents > 0 {
			f["gross"] = money.Format(read.GrossCents)
		}
		if _, err := s.store.QueueImport(ctx, store.InvoiceQueue, original.Filename, data, f); err != nil {
			s.fail(w, "queue import", err)
			return
		}
		added++
	}
	if added == 0 && len(skipped) == 0 {
		s.invoiceImportError(w, r, s.T("Bitte eine oder mehrere PDF-Dateien auswählen."))
		return
	}
	q := "?added=" + strconv.Itoa(added)
	if len(skipped) > 0 {
		q += "&skipped=" + strconv.Itoa(len(skipped))
	}
	http.Redirect(w, r, "/invoices/import/review"+q, http.StatusSeeOther)
}

func (s *Server) handleInvoiceImportReview(w http.ResponseWriter, r *http.Request) {
	notice := ""
	if n := r.URL.Query().Get("imported"); n != "" {
		notice = s.Tf("%s Rechnung(en) importiert. Die übrigen warten noch.", n)
	}
	if a := r.URL.Query().Get("added"); a != "" {
		notice = s.Tf("%s Rechnung(en) eingelesen. Bitte prüfen und die angehakten importieren.", a)
		if k := r.URL.Query().Get("skipped"); k != "" {
			notice += " " + s.Tf("%s Datei(en) übersprungen: keine PDF, oder die Warteliste ist voll (%d).", k, store.MaxQueuedImports)
		}
	}
	s.renderImportReview(w, r, http.StatusOK, nil, nil, notice)
}

// renderImportReview shows the waiting list. picked and errs, when set, are
// what the last submit ticked and what failed in it.
func (s *Server) renderImportReview(w http.ResponseWriter, r *http.Request, status int, picked map[int64]bool, errs map[int64]string, notice string) {
	ctx := r.Context()
	queued, err := s.store.ImportQueue(ctx, store.InvoiceQueue)
	if err != nil {
		s.fail(w, "import queue", err)
		return
	}
	numbers, err := s.store.InvoiceNumbers(ctx)
	if err != nil {
		s.fail(w, "invoice numbers", err)
		return
	}
	contacts, err := s.store.ListContacts(ctx)
	if err != nil {
		s.fail(w, "list contacts", err)
		return
	}
	inBatch := map[string]int{}
	for _, q := range queued {
		inBatch[strings.TrimSpace(q.Fields["number"])]++
	}
	rows := make([]queueRow, 0, len(queued))
	for _, q := range queued {
		row := queueRow{QueuedImport: q, Err: errs[q.ID]}
		n := strings.TrimSpace(q.Fields["number"])
		switch {
		case n == "":
			row.Problem = s.T("keine Rechnungsnummer gelesen")
		case numbers[n]:
			row.Problem = s.T("diese Nummer gibt es schon")
		case inBatch[n] > 1:
			row.Problem = s.T("Nummer kommt in der Liste mehrfach vor")
		case q.Fields["issue_date"] == "":
			row.Problem = s.T("kein Rechnungsdatum gelesen")
		case q.Fields["gross"] == "":
			row.Problem = s.T("kein Betrag gelesen")
		case q.Fields["contact_id"] == "" && strings.TrimSpace(q.Fields["new_contact_name"]) == "":
			row.Problem = s.T("kein Empfänger")
		}
		row.Picked = row.Problem == "" && row.Err == ""
		if picked != nil {
			row.Picked = picked[q.ID]
		}
		rows = append(rows, row)
	}
	s.render(w, status, "invoice_import_review.html", pageData{
		ImportRows: rows, Contacts: contacts, Notice: notice,
	})
}

// handleInvoiceImportReviewPost keeps what was typed, then discards or
// imports. Each row is imported on its own: one bad row does not hold up
// the others, and it stays on the list with its error.
func (s *Server) handleInvoiceImportReviewPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	queued, err := s.store.ImportQueue(ctx, store.InvoiceQueue)
	if err != nil {
		s.fail(w, "import queue", err)
		return
	}
	picked := map[int64]bool{}
	for _, v := range r.Form["pick"] {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			picked[id] = true
		}
	}
	for i, q := range queued {
		key := strconv.FormatInt(q.ID, 10)
		if _, ok := r.Form["number_"+key]; !ok {
			continue // added in another tab after this page was loaded
		}
		for _, k := range []string{"number", "issue_date", "contact_id", "new_contact_name", "gross", "vat_rate"} {
			q.Fields[k] = strings.TrimSpace(r.FormValue(k + "_" + key))
		}
		q.Fields["issue_date"] = s.parseDate(q.Fields["issue_date"])
		queued[i] = q
		if err := s.store.UpdateQueuedFields(ctx, store.InvoiceQueue, q.ID, q.Fields); err != nil {
			s.fail(w, "keep import fields", err)
			return
		}
	}

	switch d := r.FormValue("discard"); {
	case d == "all":
		if err := s.store.DiscardImportQueue(ctx, store.InvoiceQueue); err != nil {
			s.fail(w, "discard import queue", err)
			return
		}
		http.Redirect(w, r, "/invoices/import", http.StatusSeeOther)
		return
	case d != "":
		if id, err := strconv.ParseInt(d, 10, 64); err == nil {
			if err := s.store.DiscardQueuedImport(ctx, store.InvoiceQueue, id); err != nil {
				s.fail(w, "discard import", err)
				return
			}
		}
		http.Redirect(w, r, "/invoices/import/review", http.StatusSeeOther)
		return
	}

	if len(picked) == 0 {
		s.renderImportReview(w, r, http.StatusUnprocessableEntity, picked, nil, s.T("Keine Rechnung angehakt."))
		return
	}
	errs := map[int64]string{}
	created := map[string]int64{} // new customers of this submit, by name
	imported := 0
	for _, q := range queued {
		if !picked[q.ID] {
			continue
		}
		if err := s.importQueued(r, q, created); err != nil {
			errs[q.ID] = err.Error()
			continue
		}
		imported++
	}
	if len(errs) > 0 {
		s.renderImportReview(w, r, http.StatusUnprocessableEntity, picked, errs,
			s.Tf("%d importiert, %d nicht, siehe unten.", imported, len(errs)))
		return
	}
	if left, _ := s.store.ImportQueue(ctx, store.InvoiceQueue); len(left) > 0 {
		http.Redirect(w, r, "/invoices/import/review?imported="+strconv.Itoa(imported), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/invoices?imported_bulk="+strconv.Itoa(imported), http.StatusSeeOther)
}

// importQueued stores one waiting invoice like a single import: one
// position with the total, a new customer created when typed by name.
func (s *Server) importQueued(r *http.Request, q store.QueuedImport, created map[string]int64) error {
	ctx := r.Context()
	gross, err := money.Parse(q.Fields["gross"])
	if err != nil {
		return errors.New(s.T("Betrag ist keine gültige Zahl"))
	}
	rate, err := strconv.Atoi(q.Fields["vat_rate"])
	if err != nil {
		return errors.New(s.T("USt-Satz ist ungültig"))
	}
	contactID, _ := strconv.ParseInt(q.Fields["contact_id"], 10, 64)
	if name := strings.TrimSpace(q.Fields["new_contact_name"]); contactID == 0 && name != "" {
		if id, ok := created[strings.ToLower(name)]; ok {
			contactID = id
		} else {
			id, err := s.store.CreateContact(ctx, store.Contact{Kind: "customer", Name: name})
			if err != nil {
				return errors.New(s.errText(err))
			}
			created[strings.ToLower(name)], contactID = id, id
		}
	}
	if contactID == 0 {
		return errors.New(s.T("Bitte einen Rechnungsempfänger auswählen"))
	}
	_, data, err := s.store.QueuedOriginal(ctx, store.InvoiceQueue, q.ID)
	if err != nil {
		return errors.New(s.errText(err))
	}
	original, err := store.NewAttachment(q.Filename, data)
	if err != nil {
		return errors.New(s.errText(err))
	}
	_, net := money.SplitVAT(gross, rate)
	in := store.NewInvoice{
		ContactID: contactID, IssueDate: q.Fields["issue_date"], ServiceDate: q.Fields["issue_date"],
		Lines: []store.InvoiceLine{{Description: s.T("Leistung laut Rechnung"), QuantityMilli: 1000, UnitPriceCents: net, VATRate: rate}},
	}
	if _, err := s.store.ImportInvoice(ctx, store.ImportedInvoice{NewInvoice: in, Number: q.Fields["number"], Original: original, QueueID: q.ID}); err != nil {
		return errors.New(s.errText(err))
	}
	return nil
}

// handleQueuedImportEdit opens one waiting invoice in the full import form,
// for an invoice whose positions matter.
func (s *Server) handleQueuedImportEdit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	q, _, err := s.store.QueuedOriginal(r.Context(), store.InvoiceQueue, id)
	if errors.Is(err, store.ErrNotFound) {
		http.Redirect(w, r, "/invoices/import/review", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.fail(w, "queued import", err)
		return
	}
	form := map[string]string{"import": "1", "queue_id": strconv.FormatInt(q.ID, 10), "original_name": q.Filename,
		"number": q.Fields["number"], "issue_date": q.Fields["issue_date"], "service_date": q.Fields["issue_date"],
		"contact_id": q.Fields["contact_id"], "new_contact_name": q.Fields["new_contact_name"], "read_total": q.Fields["gross"]}
	var lines []lineForm
	if gross, err := money.Parse(q.Fields["gross"]); err == nil {
		rate, _ := strconv.Atoi(q.Fields["vat_rate"])
		lines = readInvoice{GrossCents: gross, VATRate: rate}.lines(s)
	}
	s.renderInvoiceForm(w, r, http.StatusOK, form, lines, q.Fields["note"])
}

// handleQueuedImportPDF shows a waiting original, so the review can be
// checked against it. Served like a Beleg: sandboxed, never as a page.
func (s *Server) handleQueuedImportPDF(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	_, data, err := s.store.QueuedOriginal(r.Context(), store.InvoiceQueue, id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "queued import", err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Write(data)
}
