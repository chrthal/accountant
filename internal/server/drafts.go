package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/chrthal/accountant/internal/match"
	"github.com/chrthal/accountant/internal/store"
)

// Booking drafts for open bank lines (ADR 0020). A draft is only a proposal:
// nothing is booked until the user confirms it, row by row or for the rows
// they tick. The ledger is append-only, so a wrong booking would need a
// Storno to undo.

// Draft sources, shown as a badge so the user knows how far to trust a row.
const (
	sourcePrevious = "wie zuletzt"
	sourceSimilar  = "ähnliche Buchung"
	sourceLLM      = "KI-Vorschlag"
)

type draft struct {
	Tx          store.BankTransaction
	Description string
	CategoryID  int64
	ContactID   int64
	VATRate     int
	Source      string // "" when nothing was proposed
	Unsure      bool
	Preselect   bool   // ticked for "Angehakte buchen"
	Why         string // where the proposal came from, for the tooltip
	// VATSource says where the rate came from. A bank line never states the
	// rate, so only an earlier booking or a configured default is a known
	// value; a model's guess is marked for checking (ADR 0021).
	VATSource string // vatFromBooking, vatFromCategory, vatStandard or vatFromModel
	// BusinessPct and BusinessNote come from an earlier mixed booking.
	BusinessPct  int
	BusinessNote string
	Err          string // set when booking this row failed
}

const (
	sourceCategory = "Kategorie erkannt"

	vatFromBooking  = "wie Buchung"
	vatFromCategory = "Kategorie"
	vatStandard     = "Standard"
	vatFromModel    = "KI"
)

// proposeDraft builds the proposal for one open bank line. The order is
// fixed: an earlier booking for the same counterparty, then the category rule
// (keywords and names), then the model, and only for what is still missing
// (ADR 0020, ADR 0021).
func (s *Server) proposeDraft(tx store.BankTransaction, settings store.Settings, prev *store.Entry,
	cats []store.Category, sug *store.BankSuggestion, like *store.Entry) draft {

	d := draft{Tx: tx, Description: tx.Counterparty}
	if d.Description == "" {
		d.Description = tx.Purpose
	}
	byID := map[int64]store.Category{}
	matchCats := make([]match.Category, 0, len(cats))
	for _, c := range cats {
		byID[c.ID] = c
		matchCats = append(matchCats, match.Category{ID: c.ID, Name: c.Name, Direction: c.Direction, Keywords: c.KeywordList()})
	}

	from := func(e *store.Entry, source, why string) {
		d.Source, d.Preselect, d.Why = source, true, why
		d.VATRate, d.VATSource = e.VATRate, vatFromBooking
		if e.CategoryID != nil {
			d.CategoryID = *e.CategoryID
		}
		if e.IsMixed() {
			d.BusinessPct, d.BusinessNote = e.BusinessPct, e.BusinessNote
		}
		if e.ContactID != nil {
			d.ContactID = *e.ContactID
		}
	}
	switch {
	case prev != nil:
		from(prev, sourcePrevious, s.Tf("wie Buchung #%d vom %s", prev.ID, s.fmtDate(prev.BookedOn)))
		d.Description = prev.Description
	case like != nil:
		// Only similar: the bank text stays, it may name what differs.
		from(like, sourceSimilar, s.Tf("ähnlich wie Buchung #%d vom %s (%s)", like.ID, s.fmtDate(like.BookedOn), like.Description))
	default:
		if hit, ok := match.CategoryFor(tx.Counterparty+" "+tx.Purpose, tx.Direction(), matchCats); ok {
			d.CategoryID, d.Source, d.Why, d.Preselect = hit.ID, sourceCategory, hit.Reason, true
		}
		if sug != nil {
			if d.Source == "" {
				d.Source = sourceLLM
				d.Why = s.Tf("vorgeschlagen von %s, bitte prüfen", sug.Model)
				d.Preselect = !sug.Unsure
				if sug.Unsure {
					d.Why = s.Tf("vorgeschlagen von %s, das Modell war sich unsicher", sug.Model)
					d.Unsure = true
				}
				if sug.CategoryID != nil {
					d.CategoryID = *sug.CategoryID
				}
			}
			if sug.Description != "" {
				d.Description = sug.Description
			}
			if sug.ContactID != nil {
				d.ContactID = *sug.ContactID
			}
			// For an expense the model's rate is never used: the configured
			// defaults apply (ADR 0021). Income keeps it, marked for checking.
			if sug.VATRate != nil && tx.Direction() == "income" {
				d.VATRate, d.VATSource = *sug.VATRate, vatFromModel
			}
		}
	}

	if d.VATSource == "" {
		if c, ok := byID[d.CategoryID]; ok && c.DefaultVATRate != nil {
			d.VATRate, d.VATSource = *c.DefaultVATRate, vatFromCategory
		} else if tx.Direction() == "outgoing" {
			d.VATRate, d.VATSource = settings.OutgoingVAT(), vatStandard
		} else {
			d.VATRate, d.VATSource = 20, vatStandard
		}
	}
	if tx.Direction() == "income" && settings.KUOn(tx.BookedOn) {
		// Under the exemption no VAT is charged (ADR 0022). An expense keeps
		// the rate on its receipt; it is recorded, not deducted.
		d.VATRate = 0
	}
	d.Description = truncate(d.Description, 200)
	return d
}

func (s *Server) buildDrafts(ctx context.Context) ([]draft, store.Settings, error) {
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return nil, settings, err
	}
	txs, err := s.store.ListBankTransactions(ctx, true, 200)
	if err != nil {
		return nil, settings, err
	}
	open, err := s.store.OpenInvoices(ctx)
	if err != nil {
		return nil, settings, err
	}
	suggestions, err := s.store.BankSuggestions(ctx)
	if err != nil {
		return nil, settings, err
	}
	cats, err := s.store.ListCategories(ctx)
	if err != nil {
		return nil, settings, err
	}
	hist, err := s.loadHistory(ctx)
	if err != nil {
		return nil, settings, err
	}

	var out []draft
	for _, tx := range txs {
		// A transfer to or from the owner is private, not a booking
		// (ADR 0035); the bank page offers to mark it so.
		if ownTransfer(tx, settings) {
			continue
		}
		// A transfer that pays an invoice belongs on the bank page, where
		// "Rechnung … bezahlt" books it with the invoice's own figures.
		payable := false
		for _, h := range store.SuggestInvoices(tx, open) {
			if h.Bookable() {
				payable = true
			}
		}
		if payable {
			continue
		}
		var prevPtr *store.Entry
		if prev, ok, err := s.store.PreviousBooking(ctx, tx); err != nil {
			return nil, settings, err
		} else if ok {
			prevPtr = &prev
		}
		var sugPtr *store.BankSuggestion
		if sug, ok := suggestions[tx.ID]; ok {
			sugPtr = &sug
		}
		var like *store.Entry
		if prevPtr == nil {
			like, _ = hist.similar(ctx, bankName(tx), tx.Direction())
		}
		out = append(out, s.proposeDraft(tx, settings, prevPtr, cats, sugPtr, like))
	}
	return out, settings, nil
}

func (s *Server) handleDrafts(w http.ResponseWriter, r *http.Request) {
	s.renderDrafts(w, r, http.StatusOK, nil, "")
}

func (s *Server) renderDrafts(w http.ResponseWriter, r *http.Request, status int, errs map[int64]string, notice string) {
	ctx := r.Context()
	drafts, settings, err := s.buildDrafts(ctx)
	if err != nil {
		s.fail(w, "build drafts", err)
		return
	}
	for i := range drafts {
		drafts[i].Err = errs[drafts[i].Tx.ID]
	}
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
	if q := r.URL.Query().Get("booked"); q != "" && notice == "" {
		notice = s.Tf("%s Buchung(en) angelegt und zugeordnet.", q)
	}
	s.render(w, status, "drafts.html", pageData{
		Drafts: drafts, Contacts: contacts, Categories: categories, Settings: settings,
		Notice: notice, LLMEnabled: s.llm != nil, LLMRunning: s.llmRunning(),
	})
}

// handleDraftsBook books the ticked rows, or the single row whose "Buchen"
// button was pressed, each with the values as the user left them.
func (s *Server) handleDraftsBook(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ids := r.Form["pick"]
	if one := r.FormValue("only"); one != "" {
		ids = []string{one}
	}
	if len(ids) == 0 {
		s.renderDrafts(w, r, http.StatusUnprocessableEntity, nil, "")
		return
	}

	ctx := r.Context()
	errs := map[int64]string{}
	booked := 0
	for _, v := range ids {
		txID, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			continue
		}
		if err := s.bookDraft(ctx, r, txID); err != nil {
			errs[txID] = s.errText(err)
			continue
		}
		booked++
	}
	if len(errs) > 0 {
		s.renderDrafts(w, r, http.StatusUnprocessableEntity, errs,
			s.Tf("%d Buchung(en) angelegt, %d nicht, siehe unten.", booked, len(errs)))
		return
	}
	http.Redirect(w, r, "/bank/drafts?booked="+strconv.Itoa(booked), http.StatusSeeOther)
}

func (s *Server) bookDraft(ctx context.Context, r *http.Request, txID int64) error {
	tx, err := s.store.GetBankTransaction(ctx, txID)
	if err != nil {
		return errors.New(s.T("Umsatz nicht gefunden"))
	}
	if tx.IsMatched() {
		return errors.New(s.T("bereits zugeordnet"))
	}
	key := strconv.FormatInt(txID, 10)
	rate, err := strconv.Atoi(r.FormValue("vat_" + key))
	if err != nil {
		return errors.New(s.T("USt-Satz fehlt"))
	}
	n := store.NewEntry{
		BookedOn:         tx.BookedOn,
		Direction:        tx.Direction(),
		Description:      strings.TrimSpace(r.FormValue("desc_" + key)),
		AmountGrossCents: tx.AbsAmountCents(),
		VATRate:          rate,
		BusinessNote:     r.FormValue("bnote_" + key),
	}
	if pct, err := businessPct(r.FormValue("bpct_" + key)); err != nil {
		return errors.New(s.T("Der betriebliche Anteil muss zwischen 1 und 100 % liegen."))
	} else if n.Direction == "outgoing" {
		n.BusinessPct = pct
	}
	if id, err := strconv.ParseInt(r.FormValue("cat_"+key), 10, 64); err == nil && id > 0 {
		n.CategoryID = &id
	}
	if id, err := strconv.ParseInt(r.FormValue("contact_"+key), 10, 64); err == nil && id > 0 {
		n.ContactID = &id
	}
	id, err := s.store.CreateEntry(ctx, n)
	if err != nil {
		return err
	}
	if err := s.store.MatchBankTransaction(ctx, txID, id); err != nil {
		return errors.New(s.Tf("Buchung #%d angelegt, Zuordnung fehlgeschlagen: %s", id, s.errText(err)))
	}
	return nil
}

// bankName is what a bank line is recognised by: the counterparty, or the
// purpose where a card payment names the shop only there.
func bankName(tx store.BankTransaction) string {
	if strings.TrimSpace(tx.Counterparty) != "" {
		return tx.Counterparty
	}
	return tx.Purpose
}
