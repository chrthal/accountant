package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/chrthal/accountant/internal/match"
	"github.com/chrthal/accountant/internal/money"
	"github.com/chrthal/accountant/internal/ocr"
	"github.com/chrthal/accountant/internal/store"
)

// Bulk upload of Belege (ADR 0033), the counterpart of the bulk invoice
// import (ADR 0032): many receipts onto a waiting list, each read as a
// single receipt is read, checked in one list, the ticked ones booked. Each
// booking is written with its Beleg in one transaction.

var receiptFields = []string{"booked_on", "direction", "description", "amount", "vat_rate", "category_id", "contact_id",
	"business_pct", "business_note"}

// handleReceiptBatch puts the uploaded receipts onto the waiting list, one
// file at a time from the request, and starts reading them in the
// background (ADR 0042).
func (s *Server) handleReceiptBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	queued, err := s.store.ImportQueue(ctx, store.ReceiptQueue)
	if err != nil {
		s.fail(w, "receipt queue", err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(store.MaxQueuedImports)*(store.MaxAttachmentSize+1<<16))
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	added, skipped, room := 0, 0, store.MaxQueuedImports-len(queued)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.receiptError(w, s.T("Die Dateien konnten nicht vollständig hochgeladen werden."))
			return
		}
		if part.FormName() != "photos" || part.FileName() == "" {
			part.Close()
			continue
		}
		name := part.FileName()
		data, err := io.ReadAll(io.LimitReader(part, store.MaxAttachmentSize+1))
		part.Close()
		if err != nil {
			s.receiptError(w, s.T("Die Dateien konnten nicht vollständig hochgeladen werden."))
			return
		}
		a, err := store.NewAttachment(name, data)
		if err != nil || added >= room {
			skipped++
			continue
		}
		fields := map[string]string{pendingField: "1", "sha256": a.SHA256, "direction": "outgoing"}
		if _, err := s.store.QueueImport(ctx, store.ReceiptQueue, a.Filename, data, fields); err != nil {
			s.fail(w, "queue receipt", err)
			return
		}
		added++
	}
	if added > 0 {
		s.startReading()
	}
	if added == 0 && skipped == 0 {
		s.receiptError(w, s.T("Bitte ein oder mehrere Fotos oder PDF-Rechnungen auswählen."))
		return
	}
	q := "?added=" + strconv.Itoa(added)
	if skipped > 0 {
		q += "&skipped=" + strconv.Itoa(skipped)
	}
	http.Redirect(w, r, "/receipts/review"+q, http.StatusSeeOther)
}

// proposeReceipt is receiptProposal with an earlier booking of a similar
// vendor looked up first (ADR 0044).
func (s *Server) proposeReceipt(ctx context.Context, h history, d ocr.Draft, note string, a store.Attachment,
	settings store.Settings, cats []store.Category, contacts []store.Contact) map[string]string {

	like, ok := h.similar(ctx, d.Vendor, "outgoing")
	why := ""
	if ok {
		why = s.Tf("ähnlich wie Buchung #%d vom %s (%s)", like.ID, s.fmtDate(like.BookedOn), like.Description)
	}
	return receiptProposal(d, note, a, settings, cats, contacts, like, why)
}

// receiptProposal is the booking a read Beleg suggests. Category, contact
// and business share come from like, an earlier booking of a similar vendor
// (ADR 0044), else from the category keywords (ADR 0021); the VAT rate is
// the one printed on the receipt, else like's, else the category's, else
// the standard rate. why says where it came from.
func receiptProposal(d ocr.Draft, note string, a store.Attachment, settings store.Settings,
	cats []store.Category, contacts []store.Contact, like *store.Entry, why string) map[string]string {

	f := map[string]string{"direction": "outgoing", "booked_on": d.BookedOn, "sha256": a.SHA256, "note": note,
		"description": truncate(d.Description(), 200)}
	if d.AmountCents > 0 {
		f["amount"] = money.Format(d.AmountCents)
	}
	if like != nil {
		f["why"] = why
		if like.CategoryID != nil {
			f["category_id"] = strconv.FormatInt(*like.CategoryID, 10)
		}
		if like.ContactID != nil {
			f["contact_id"] = strconv.FormatInt(*like.ContactID, 10)
		}
		if like.IsMixed() {
			f["business_pct"], f["business_note"] = strconv.Itoa(like.BusinessPct), like.BusinessNote
		}
		if f["description"] == "" {
			f["description"] = like.Description
		}
		f["vat_rate"] = strconv.Itoa(like.VATRate)
		if d.VATRateRead {
			f["vat_rate"] = strconv.Itoa(d.VATRate)
		}
		return f
	}
	rate := settings.OutgoingVAT()
	matchCats := make([]match.Category, 0, len(cats))
	for _, c := range cats {
		matchCats = append(matchCats, match.Category{ID: c.ID, Name: c.Name, Direction: c.Direction, Keywords: c.KeywordList()})
	}
	if hit, ok := match.CategoryFor(d.Vendor+" "+d.Description(), "outgoing", matchCats); ok {
		f["category_id"] = strconv.FormatInt(hit.ID, 10)
		for _, c := range cats {
			if c.ID == hit.ID && c.DefaultVATRate != nil {
				rate = *c.DefaultVATRate
			}
		}
	}
	if d.VATRateRead {
		rate = d.VATRate
	}
	f["vat_rate"] = strconv.Itoa(rate)
	if d.Vendor != "" {
		for _, c := range contacts {
			if match.SimilarName(d.Vendor, c.Name) {
				f["contact_id"] = strconv.FormatInt(c.ID, 10)
				break
			}
		}
	}
	return f
}

func (s *Server) handleReceiptReview(w http.ResponseWriter, r *http.Request) {
	notice := ""
	if n := r.URL.Query().Get("booked"); n != "" {
		notice = s.Tf("%s Beleg(e) gebucht. Die übrigen warten noch.", n)
	}
	if a := r.URL.Query().Get("added"); a != "" {
		notice = s.Tf("%s Beleg(e) hochgeladen. Sie werden jetzt gelesen; danach bitte prüfen und die angehakten buchen.", a)
		if k := r.URL.Query().Get("skipped"); k != "" {
			notice += " " + s.Tf("%s Datei(en) übersprungen: kein Foto und keine PDF, zu groß, oder die Warteliste ist voll (%d).", k, store.MaxQueuedImports)
		}
	}
	s.renderReceiptReview(w, r, http.StatusOK, nil, nil, notice)
}

func (s *Server) renderReceiptReview(w http.ResponseWriter, r *http.Request, status int, picked map[int64]bool, errs map[int64]string, notice string) {
	ctx := r.Context()
	queued, err := s.store.ImportQueue(ctx, store.ReceiptQueue)
	if err != nil {
		s.fail(w, "receipt queue", err)
		return
	}
	booked, err := s.store.ReceiptHashes(ctx)
	if err != nil {
		s.fail(w, "receipt hashes", err)
		return
	}
	cats, err := s.store.ListCategories(ctx)
	if err != nil {
		s.fail(w, "list categories", err)
		return
	}
	contacts, err := s.store.ListContacts(ctx)
	if err != nil {
		s.fail(w, "list contacts", err)
		return
	}
	inList := map[string]int{}
	for _, q := range queued {
		inList[q.Fields["sha256"]]++
	}
	rows := make([]queueRow, 0, len(queued))
	pending := 0
	for _, q := range queued {
		row := queueRow{QueuedImport: q, Err: errs[q.ID], Pending: q.Fields[pendingField] != ""}
		switch {
		case row.Pending:
			pending++
			row.Problem = s.T("wird gelesen …")
		case booked[q.Fields["sha256"]] != 0:
			row.Problem = s.Tf("dieser Beleg ist schon gebucht (Buchung #%d)", booked[q.Fields["sha256"]])
		case inList[q.Fields["sha256"]] > 1:
			row.Problem = s.T("dieselbe Datei ist mehrfach in der Liste")
		case q.Fields["amount"] == "":
			row.Problem = s.T("kein Betrag gelesen")
		case q.Fields["booked_on"] == "":
			row.Problem = s.T("kein Datum gelesen")
		case strings.TrimSpace(q.Fields["description"]) == "":
			row.Problem = s.T("keine Beschreibung")
		}
		row.Picked = row.Problem == "" && row.Err == ""
		if picked != nil && !row.Pending {
			row.Picked = picked[q.ID]
		}
		rows = append(rows, row)
	}
	// A restart stops a run; the next look at the list picks it up again.
	if pending > 0 && !s.readingReceipts() {
		s.startReading()
	}
	s.render(w, status, "receipt_review.html", pageData{ImportRows: rows, Categories: cats, Contacts: contacts, Notice: notice,
		Pending: pending})
}

// handleReceiptReviewPost keeps what was typed, then discards or books.
// Each ticked row is booked on its own, with its Beleg, in one transaction.
func (s *Server) handleReceiptReviewPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	queued, err := s.store.ImportQueue(ctx, store.ReceiptQueue)
	if err != nil {
		s.fail(w, "receipt queue", err)
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
		if _, ok := r.Form["amount_"+key]; !ok {
			continue // added in another tab after this page was loaded
		}
		for _, k := range receiptFields {
			q.Fields[k] = strings.TrimSpace(r.FormValue(k + "_" + key))
		}
		q.Fields["booked_on"] = s.parseDate(q.Fields["booked_on"])
		queued[i] = q
		if err := s.store.UpdateQueuedFields(ctx, store.ReceiptQueue, q.ID, q.Fields); err != nil {
			s.fail(w, "keep receipt fields", err)
			return
		}
	}

	switch d := r.FormValue("discard"); {
	case d == "all":
		if err := s.store.DiscardImportQueue(ctx, store.ReceiptQueue); err != nil {
			s.fail(w, "discard receipt queue", err)
			return
		}
		http.Redirect(w, r, "/receipts/new", http.StatusSeeOther)
		return
	case d != "":
		if id, err := strconv.ParseInt(d, 10, 64); err == nil {
			if err := s.store.DiscardQueuedImport(ctx, store.ReceiptQueue, id); err != nil {
				s.fail(w, "discard receipt", err)
				return
			}
		}
		http.Redirect(w, r, "/receipts/review", http.StatusSeeOther)
		return
	}

	if len(picked) == 0 {
		s.renderReceiptReview(w, r, http.StatusUnprocessableEntity, picked, nil, s.T("Kein Beleg angehakt."))
		return
	}
	errs := map[int64]string{}
	booked := 0
	for _, q := range queued {
		if !picked[q.ID] {
			continue
		}
		if err := s.bookQueuedReceipt(r, q); err != nil {
			errs[q.ID] = err.Error()
			continue
		}
		booked++
	}
	if len(errs) > 0 {
		s.renderReceiptReview(w, r, http.StatusUnprocessableEntity, picked, errs,
			s.Tf("%d gebucht, %d nicht, siehe unten.", booked, len(errs)))
		return
	}
	if left, _ := s.store.ImportQueue(ctx, store.ReceiptQueue); len(left) > 0 {
		http.Redirect(w, r, "/receipts/review?booked="+strconv.Itoa(booked), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/entries?booked_bulk="+strconv.Itoa(booked), http.StatusSeeOther)
}

func (s *Server) bookQueuedReceipt(r *http.Request, q store.QueuedImport) error {
	ctx := r.Context()
	gross, err := money.Parse(q.Fields["amount"])
	if err != nil {
		return errors.New(s.T("Betrag ist keine gültige Zahl"))
	}
	rate, err := strconv.Atoi(q.Fields["vat_rate"])
	if err != nil {
		return errors.New(s.T("USt-Satz ist ungültig"))
	}
	n := store.NewEntry{BookedOn: q.Fields["booked_on"], Direction: q.Fields["direction"],
		Description: q.Fields["description"], AmountGrossCents: gross, VATRate: rate, BusinessNote: q.Fields["business_note"]}
	if pct, err := businessPct(q.Fields["business_pct"]); err != nil {
		return errors.New(s.T("Der betriebliche Anteil muss zwischen 1 und 100 % liegen."))
	} else if n.Direction == "outgoing" {
		n.BusinessPct = pct
	}
	if id, err := strconv.ParseInt(q.Fields["category_id"], 10, 64); err == nil && id > 0 {
		n.CategoryID = &id
	}
	if id, err := strconv.ParseInt(q.Fields["contact_id"], 10, 64); err == nil && id > 0 {
		n.ContactID = &id
	}
	_, data, err := s.store.QueuedOriginal(ctx, store.ReceiptQueue, q.ID)
	if err != nil {
		return errors.New(s.errText(err))
	}
	a, err := store.NewAttachment(q.Filename, data)
	if err != nil {
		return errors.New(s.errText(err))
	}
	if _, err := s.store.CreateEntryWithReceipt(ctx, n, a, q.ID); err != nil {
		return errors.New(s.errText(err))
	}
	return nil
}

// handleQueuedReceiptEdit opens one waiting Beleg in the ordinary booking
// form, e.g. to match it to a bank line or to see the recognised text.
func (s *Server) handleQueuedReceiptEdit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	q, _, err := s.store.QueuedOriginal(r.Context(), store.ReceiptQueue, id)
	if errors.Is(err, store.ErrNotFound) {
		http.Redirect(w, r, "/receipts/review", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.fail(w, "queued receipt", err)
		return
	}
	form := map[string]string{"receipt_queue_id": strconv.FormatInt(q.ID, 10), "receipt_name": q.Filename}
	for _, k := range receiptFields {
		form[k] = q.Fields[k]
	}
	s.renderEntryForm(w, r, http.StatusOK, form, q.Fields["note"], nil)
}

// handleQueuedReceiptFile shows a waiting Beleg as it was uploaded: inline
// for the review's thumbnails, sandboxed like a stored Beleg.
func (s *Server) handleQueuedReceiptFile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	q, data, err := s.store.QueuedOriginal(r.Context(), store.ReceiptQueue, id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "queued receipt", err)
		return
	}
	a, err := store.NewAttachment(q.Filename, data)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", a.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'; style-src 'unsafe-inline'")
	w.Write(data)
}
