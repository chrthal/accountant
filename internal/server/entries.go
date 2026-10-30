package server

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chrthal/accountant/internal/money"
	"github.com/chrthal/accountant/internal/ocr"
	"github.com/chrthal/accountant/internal/store"
)

// maxEntryBody bounds a booking form post: one Beleg of up to 10 MiB, which
// may arrive base64-encoded from the OCR step (about 13.4 MiB), plus fields.
const maxEntryBody = 16 << 20

func (s *Server) handleEntryForm(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	direction := q.Get("direction")
	if direction != "income" {
		direction = "outgoing"
	}
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	rate := 20
	if direction == "outgoing" {
		rate = settings.OutgoingVAT()
	} else if settings.KUOn(time.Now().Format("2006-01-02")) {
		rate = 0
	}
	form := map[string]string{
		"booked_on": time.Now().Format("2006-01-02"),
		"direction": direction,
		"vat_rate":  strconv.Itoa(rate),
	}

	// "Buchung aus Umsatz": the bank line already says when, how much and
	// which way. Only the VAT rate and the category are left to the user, and
	// saving matches the booking to the transaction.
	if id, err := strconv.ParseInt(q.Get("bank_tx"), 10, 64); err == nil {
		tx, err := s.store.GetBankTransaction(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			s.fail(w, "get bank transaction", err)
			return
		}
		form["bank_tx_id"] = strconv.FormatInt(tx.ID, 10)
		form["booked_on"] = tx.BookedOn
		form["direction"] = tx.Direction()
		form["amount"] = money.Format(tx.AbsAmountCents())
		desc := tx.Counterparty
		if desc == "" {
			desc = tx.Purpose
		}
		form["description"] = truncate(desc, 200)
		// Same counterparty as an earlier booking: start from that one
		// (ADR 0020). The user still sees and saves the form.
		if prev, ok, err := s.store.PreviousBooking(r.Context(), tx); err == nil && ok {
			form["description"] = prev.Description
			form["vat_rate"] = strconv.Itoa(prev.VATRate)
			if prev.CategoryID != nil {
				form["category_id"] = strconv.FormatInt(*prev.CategoryID, 10)
			}
			if prev.ContactID != nil {
				form["contact_id"] = strconv.FormatInt(*prev.ContactID, 10)
			}
			// A monthly phone bill keeps last month's business share.
			if prev.IsMixed() {
				form["business_pct"] = strconv.Itoa(prev.BusinessPct)
				form["business_note"] = prev.BusinessNote
			}
			form["prefill_note"] = s.Tf("Vorausgefüllt wie Buchung #%d vom %s.", prev.ID, s.fmtDate(prev.BookedOn))
		} else if cats, err := s.store.ListCategories(r.Context()); err == nil {
			// No earlier booking for this counterparty: a similar one
			// (ADR 0044), else the category rule and the VAT default that
			// goes with it (ADR 0021).
			var like *store.Entry
			if hist, err := s.loadHistory(r.Context()); err == nil {
				like, _ = hist.similar(r.Context(), bankName(tx), tx.Direction())
			}
			d := s.proposeDraft(tx, settings, nil, cats, nil, like)
			form["vat_rate"] = strconv.Itoa(d.VATRate)
			if d.ContactID > 0 {
				form["contact_id"] = strconv.FormatInt(d.ContactID, 10)
			}
			if d.BusinessPct > 0 {
				form["business_pct"], form["business_note"] = strconv.Itoa(d.BusinessPct), d.BusinessNote
			}
			if d.CategoryID > 0 {
				form["category_id"] = strconv.FormatInt(d.CategoryID, 10)
				form["prefill_note"] = s.Tf("Kategorie erkannt (%s).", d.Why)
				if d.Source == sourceSimilar {
					form["prefill_note"] = s.Tf("Vorausgefüllt %s.", d.Why)
				}
			}
		}
	}
	s.renderEntryForm(w, r, http.StatusOK, form, "", nil)
}

// renderEntryForm shows the booking form with its dropdowns and, when the
// form carries a bank transaction, that transaction beside it.
func (s *Server) renderEntryForm(w http.ResponseWriter, r *http.Request, status int,
	form map[string]string, errMsg string, draft *ocr.Draft) {

	ctx := r.Context()
	contacts, err := s.store.ListContacts(ctx)
	if err != nil {
		s.fail(w, "list contacts", err)
		return
	}
	categories, err := s.store.ListCategories(ctx)
	if err != nil {
		s.fail(w, "list categories", err)
		return
	}
	var bank []store.BankTransaction
	if id, err := strconv.ParseInt(form["bank_tx_id"], 10, 64); err == nil {
		if tx, err := s.store.GetBankTransaction(ctx, id); err == nil {
			bank = []store.BankTransaction{tx}
		}
	}
	if form["business_pct"] == "" {
		form["business_pct"] = "100"
	}
	s.render(w, status, "entry_form.html", pageData{
		Contacts: contacts, Categories: categories, Form: form, Error: errMsg,
		OCR: draft, Transactions: bank,
	})
}

func (s *Server) handleEntryCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxEntryBody)
	if err := r.ParseMultipartForm(8 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		http.Error(w, s.T("Das Formular ist zu groß oder unvollständig (Beleg höchstens 10 MiB)."),
			http.StatusRequestEntityTooLarge)
		return
	} else if errors.Is(err, http.ErrNotMultipart) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
	}

	form := map[string]string{}
	for _, f := range []string{"booked_on", "direction", "description", "amount",
		"vat_rate", "konto", "gegenkonto", "contact_id", "category_id",
		"bank_tx_id", "receipt_payload", "receipt_name", "receipt_queue_id", "business_pct", "business_note"} {
		form[f] = r.FormValue(f)
	}
	form["booked_on"] = s.parseDate(form["booked_on"])
	fail := func(msg string) { s.renderEntryForm(w, r, http.StatusUnprocessableEntity, form, msg, nil) }

	gross, err := money.Parse(form["amount"])
	if err != nil {
		fail(s.T("Betrag ist keine gültige Zahl (z. B. 1.234,56)"))
		return
	}
	rate, err := strconv.Atoi(form["vat_rate"])
	if err != nil {
		fail(s.T("USt-Satz ist ungültig"))
		return
	}

	pct, err := businessPct(form["business_pct"])
	if err != nil {
		fail(s.T("Der betriebliche Anteil muss zwischen 1 und 100 % liegen."))
		return
	}
	if form["direction"] == "income" {
		pct = 100
	}

	// The Beleg is validated before the booking is written, so a rejected file
	// never leaves a booking behind without the receipt it was meant to have.
	receipt, err := receiptFromForm(r, form)
	if err != nil {
		fail(s.Tf("Beleg: %s", s.errText(err)))
		return
	}

	n := store.NewEntry{
		BookedOn:         form["booked_on"],
		Direction:        form["direction"],
		Description:      form["description"],
		AmountGrossCents: gross,
		VATRate:          rate,
		Konto:            form["konto"],
		Gegenkonto:       form["gegenkonto"],
		BusinessPct:      pct,
		BusinessNote:     form["business_note"],
	}
	if id, err := strconv.ParseInt(form["contact_id"], 10, 64); err == nil && id > 0 {
		n.ContactID = &id
	}
	// The category is what supplies blank account tags (ADR 0012), so it is
	// resolved in the store rather than here.
	if id, err := strconv.ParseInt(form["category_id"], 10, 64); err == nil && id > 0 {
		n.CategoryID = &id
	}

	// A Beleg from the bulk waiting list is booked with it in one
	// transaction, and leaves the list (ADR 0033).
	var id int64
	queueID, _ := strconv.ParseInt(form["receipt_queue_id"], 10, 64)
	if queueID != 0 {
		q, data, err := s.store.QueuedOriginal(r.Context(), store.ReceiptQueue, queueID)
		if err != nil {
			fail(s.T("Der Beleg ist nicht mehr auf der Warteliste. Vielleicht wurde er inzwischen gebucht."))
			return
		}
		a, err := store.NewAttachment(q.Filename, data)
		if err != nil {
			fail(s.Tf("Beleg: %s", s.errText(err)))
			return
		}
		if id, err = s.store.CreateEntryWithReceipt(r.Context(), n, a, queueID); err != nil {
			fail(s.errText(err))
			return
		}
		receipt = nil
	} else if id, err = s.store.CreateEntry(r.Context(), n); err != nil {
		fail(s.errText(err))
		return
	}

	// The booking exists from here on. What follows adds to it, and a
	// failure is reported on the booking's page rather than as a lost form.
	var warn []string
	if receipt != nil {
		if _, err := s.store.AddAttachment(r.Context(), id, *receipt); err != nil {
			log.Printf("attach receipt to %d: %v", id, err)
			warn = append(warn, "beleg")
		}
	}
	if txID, err := strconv.ParseInt(form["bank_tx_id"], 10, 64); err == nil {
		if err := s.store.MatchBankTransaction(r.Context(), txID, id); err != nil {
			log.Printf("match new entry %d to tx %d: %v", id, txID, err)
			warn = append(warn, "bank")
		}
	}
	loc := "/entries/" + strconv.FormatInt(id, 10)
	if len(warn) > 0 {
		loc += "?warn=" + strings.Join(warn, ",")
	}
	http.Redirect(w, r, loc, http.StatusSeeOther)
}

// receiptFromForm reads the Beleg of a booking form: an uploaded file, or the
// photo carried over from the OCR step. It returns nil when there is none.
func receiptFromForm(r *http.Request, form map[string]string) (*store.Attachment, error) {
	var (
		name string
		data []byte
	)
	if r.MultipartForm != nil {
		if file, header, err := r.FormFile("receipt"); err == nil {
			defer file.Close()
			data, err = io.ReadAll(io.LimitReader(file, store.MaxAttachmentSize+1))
			if err != nil {
				return nil, fmt.Errorf("could not be read")
			}
			name = header.Filename
		}
	}
	if len(data) == 0 && form["receipt_payload"] != "" {
		var err error
		data, err = base64.StdEncoding.DecodeString(form["receipt_payload"])
		if err != nil {
			return nil, fmt.Errorf("the photo from the receipt step was lost — please attach it again")
		}
		name = form["receipt_name"]
	}
	if len(data) == 0 {
		return nil, nil
	}
	a, err := store.NewAttachment(name, data)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Server) handleAttachmentAdd(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxEntryBody)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		s.renderEntry(w, r, id, http.StatusRequestEntityTooLarge, s.T("Der Beleg ist zu groß (höchstens 10 MiB)."))
		return
	}
	a, err := receiptFromForm(r, map[string]string{})
	if err == nil && a == nil {
		err = fmt.Errorf("please choose a file")
	}
	if err != nil {
		s.renderEntry(w, r, id, http.StatusUnprocessableEntity, s.Tf("Beleg: %s", s.errText(err)))
		return
	}
	if _, err := s.store.AddAttachment(r.Context(), id, *a); errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "add attachment", err)
		return
	}
	http.Redirect(w, r, "/entries/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// handleAttachment serves a stored Beleg. The type was sniffed on upload and
// only images and PDFs get in, but the response is locked down anyway: no
// sniffing, and a sandbox CSP, so a crafted file cannot run as a page.
func (s *Server) handleAttachment(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a, err := s.store.GetAttachment(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "get attachment", err)
		return
	}
	name := a.Filename
	if name == "" {
		name = fmt.Sprintf("Beleg-%d", a.ID)
	}
	w.Header().Set("Content-Type", a.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'; style-src 'unsafe-inline'")
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	disposition := "inline"
	if r.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, sanitizeFilename(name)))
	w.Write(a.Data)
}

func sanitizeFilename(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == '"' || r == '\\' || r == 0x7f {
			return '_'
		}
		return r
	}, s)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// businessPct reads a business share from a form (ADR 0041): empty is 100.
func businessPct(v string) (int, error) {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "%"))
	if v == "" {
		return 100, nil
	}
	pct, err := strconv.Atoi(v)
	if err != nil || pct < 1 || pct > 100 {
		return 0, fmt.Errorf("business share %q", v)
	}
	return pct, nil
}
