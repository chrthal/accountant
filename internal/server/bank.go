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

	"github.com/chrthal/accountant/internal/bankcsv"
	"github.com/chrthal/accountant/internal/match"
	"github.com/chrthal/accountant/internal/store"
)

// colCtx is what the "col-select" template needs for one mapping dropdown.
type colCtx struct {
	Name, Label string
	Columns     []string
	Selected    string
	Optional    bool
}

// Import is two steps: upload, then map the columns. The file is carried
// through the mapping form rather than parked on the server, so an abandoned
// import leaves nothing behind (ADR 0009).

func (s *Server) handleBank(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	onlyUnmatched := r.URL.Query().Get("all") != "1"

	transactions, err := s.store.ListBankTransactions(ctx, onlyUnmatched, 200)
	if err != nil {
		log.Printf("list bank transactions: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	candidates, err := s.store.UnmatchedEntries(ctx, 500)
	if err != nil {
		log.Printf("unmatched entries: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.suggest(ctx, transactions, candidates); err != nil {
		s.fail(w, "bank suggestions", err)
		return
	}
	notice := ""
	if n := r.URL.Query().Get("auto"); n != "" {
		notice = s.Tf("%s Zahlungseingänge eindeutig zugeordnet und gebucht.", n)
	}

	if n := r.URL.Query().Get("private"); n != "" {
		notice = s.Tf("%s Umsatz/Umsätze als privat markiert. Sie zählen nicht zu Einnahmen und Ausgaben.", n)
	}
	if n := r.URL.Query().Get("converted"); n != "" {
		notice = s.Tf("%s Umsatz/Umsätze als privat umgebucht: die Buchungen sind storniert, die Umsätze als privat markiert.", n)
		if k := r.URL.Query().Get("kept"); k != "" {
			notice += " " + s.Tf("%s blieben, weil sie eine Rechnungszahlung oder eine Anlage sind.", k)
		}
	}
	own := 0
	for _, t := range transactions {
		if t.OwnTransfer && !t.IsMatched() {
			own++
		}
	}
	ownBooked, err := s.ownBookedTransfers(ctx)
	if err != nil {
		s.fail(w, "own booked transfers", err)
		return
	}

	s.render(w, http.StatusOK, "bank.html", pageData{
		Transactions: transactions,
		Entries:      candidates,
		Notice:       notice,
		SureCount:    len(sureInvoicePairs(transactions)),
		OwnCount:     own,
		OwnBooked:    len(ownBooked),
		Form:         map[string]string{"all": r.URL.Query().Get("all")},
	})
}

// ownTransfer reports whether a line goes to or comes from the owner: the
// counterparty is the issuer's own name, in any word order. Such a line is a
// Privatentnahme or -einlage, not income or expense (ADR 0035).
func ownTransfer(tx store.BankTransaction, settings store.Settings) bool {
	return settings.IssuerName != "" && tx.Counterparty != "" && match.SimilarName(tx.Counterparty, settings.IssuerName)
}

func (s *Server) handleBankPrivate(w http.ResponseWriter, r *http.Request) {
	txID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad transaction id", http.StatusBadRequest)
		return
	}
	switch err := s.store.MarkPrivate(r.Context(), txID); {
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case err != nil:
		s.bankError(w, r, s.errText(err))
	default:
		http.Redirect(w, r, "/bank?private=1", http.StatusSeeOther)
	}
}

func (s *Server) handleBankUnprivate(w http.ResponseWriter, r *http.Request) {
	txID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad transaction id", http.StatusBadRequest)
		return
	}
	if err := s.store.UnmarkPrivate(r.Context(), txID); err != nil {
		s.fail(w, "unmark private", err)
		return
	}
	http.Redirect(w, r, "/bank?all=1", http.StatusSeeOther)
}

// ownBookedTransfers lists the transfers to or from the owner that were
// matched to bookings: income or expense where none was (ADR 0035).
func (s *Server) ownBookedTransfers(ctx context.Context) ([]store.BankTransaction, error) {
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return nil, err
	}
	all, err := s.store.ListBankTransactions(ctx, false, 2000)
	if err != nil {
		return nil, err
	}
	var out []store.BankTransaction
	for _, tx := range all {
		if !tx.IsMatched() || !ownTransfer(tx, settings) {
			continue
		}
		// An invoice payment or an asset belongs to the books; such a line
		// is not offered, so the warning does not stay for good.
		keep := false
		for _, e := range tx.Matches {
			if e.InvoiceID != nil {
				keep = true
			} else if _, isAsset, err := s.store.AssetForEntry(ctx, e.ID); err != nil {
				return nil, err
			} else if isAsset {
				keep = true
			}
		}
		if !keep {
			out = append(out, tx)
		}
	}
	return out, nil
}

// handleBankToPrivate turns one booked bank line into a private transfer:
// Storno of its bookings, match removed, marked private, in one step.
func (s *Server) handleBankToPrivate(w http.ResponseWriter, r *http.Request) {
	txID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad transaction id", http.StatusBadRequest)
		return
	}
	switch _, err := s.store.ConvertToPrivate(r.Context(), txID); {
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case err != nil:
		s.bankError(w, r, s.errText(err))
	default:
		http.Redirect(w, r, "/bank?all=1&converted=1", http.StatusSeeOther)
	}
}

// handleBankToPrivateOwn does that for every booked transfer to or from the
// owner. Lines that cannot be converted (an invoice payment, an asset) stay
// and are counted.
func (s *Server) handleBankToPrivateOwn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	txs, err := s.ownBookedTransfers(ctx)
	if err != nil {
		s.fail(w, "own booked transfers", err)
		return
	}
	converted, kept := 0, 0
	for _, tx := range txs {
		if _, err := s.store.ConvertToPrivate(ctx, tx.ID); err != nil {
			kept++
			continue
		}
		converted++
	}
	loc := "/bank?all=1&converted=" + strconv.Itoa(converted)
	if kept > 0 {
		loc += "&kept=" + strconv.Itoa(kept)
	}
	http.Redirect(w, r, loc, http.StatusSeeOther)
}

// handleBankPrivateOwn marks every open transfer to or from the owner as
// private in one step.
func (s *Server) handleBankPrivateOwn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settings, err := s.store.Settings(ctx)
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	txs, err := s.store.ListBankTransactions(ctx, true, 500)
	if err != nil {
		s.fail(w, "list bank transactions", err)
		return
	}
	n := 0
	for _, tx := range txs {
		if !ownTransfer(tx, settings) {
			continue
		}
		if err := s.store.MarkPrivate(ctx, tx.ID); err != nil {
			s.fail(w, "mark private", err)
			return
		}
		n++
	}
	http.Redirect(w, r, "/bank?private="+strconv.Itoa(n), http.StatusSeeOther)
}

// suggest attaches likely matches to each open transaction: bookings of the
// same amount and direction, and open invoices of the same amount.
func (s *Server) suggest(ctx context.Context, txs []store.BankTransaction, candidates []store.Entry) error {
	open, err := s.store.OpenInvoices(ctx)
	if err != nil {
		return err
	}
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return err
	}
	for i := range txs {
		if txs[i].IsPrivate() {
			continue
		}
		txs[i].OwnTransfer = ownTransfer(txs[i], settings)
		if txs[i].IsMatched() {
			continue
		}
		txs[i].Suggestions = store.SuggestMatches(txs[i], candidates)
		txs[i].InvoiceSuggestions = store.SuggestInvoices(txs[i], open)
	}
	return nil
}

// handleBankUpload reads the file and shows the column-mapping step.
func (s *Server) handleBankUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(bankcsv.MaxFileSize); err != nil {
		s.bankError(w, r, s.T("Die Datei konnte nicht gelesen werden (maximal 2 MiB)."))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		s.bankError(w, r, s.T("Bitte eine CSV-Datei auswählen."))
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, bankcsv.MaxFileSize+1))
	if err != nil {
		s.bankError(w, r, s.T("Die Datei konnte nicht gelesen werden."))
		return
	}

	parsed, err := bankcsv.Parse(raw)
	if err != nil {
		s.bankError(w, r, s.errText(err))
		return
	}

	// Show the first rows so the user can see which column is which.
	preview := parsed.Records
	if len(preview) > 6 {
		preview = preview[:6]
	}

	// Preselect the mapping: the one confirmed last time for this layout if
	// there is one, otherwise a guess from the values (ADR 0009 addendum,
	// plan 0002 Phase 9). Either way the user sees and confirms it.
	m := parsed.Guess()
	hint := "erkannt"
	if saved, ok, err := s.store.SavedBankMapping(r.Context(), parsed.Signature(m.HasHeader)); err != nil {
		s.fail(w, "saved bank mapping", err)
		return
	} else if ok {
		m, hint = saved, "wie beim letzten Import"
	}

	form := mappingForm(m, hint)
	form["source_name"] = header.Filename
	form["payload"] = base64.StdEncoding.EncodeToString(raw)
	s.render(w, http.StatusOK, "bank_mapping.html", pageData{
		Columns: parsed.Columns(m.HasHeader),
		Preview: preview,
		Form:    form,
	})
}

// mappingForm turns a mapping into the mapping page's preselected values.
func mappingForm(m bankcsv.Mapping, hint string) map[string]string {
	header := ""
	if m.HasHeader {
		header = "1"
	}
	return map[string]string{
		"col_date":         strconv.Itoa(m.Date),
		"col_amount":       strconv.Itoa(m.Amount),
		"col_counterparty": strconv.Itoa(m.Counterparty),
		"col_purpose":      strconv.Itoa(m.Purpose),
		"has_header":       header,
		"header_hint":      hint,
	}
}

// handleBankImport applies the mapping and stores the rows.
func (s *Server) handleBankImport(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.bankError(w, r, s.T("Formular konnte nicht gelesen werden."))
		return
	}

	raw, err := base64.StdEncoding.DecodeString(r.FormValue("payload"))
	if err != nil {
		s.bankError(w, r, s.T("Die hochgeladene Datei ist verloren gegangen — bitte erneut hochladen."))
		return
	}
	parsed, err := bankcsv.Parse(raw)
	if err != nil {
		s.bankError(w, r, s.errText(err))
		return
	}

	column := func(name string) int {
		v, err := strconv.Atoi(r.FormValue(name))
		if err != nil {
			return -1
		}
		return v
	}
	mapping := bankcsv.Mapping{
		Date:         column("col_date"),
		Amount:       column("col_amount"),
		Counterparty: column("col_counterparty"),
		Purpose:      column("col_purpose"),
		HasHeader:    r.FormValue("has_header") == "1",
	}

	rows, skipped, err := parsed.MapRows(mapping)
	if err != nil {
		// Re-show the mapping step with the file intact rather than making the
		// user upload it again.
		preview := parsed.Records
		if len(preview) > 6 {
			preview = preview[:6]
		}
		form := mappingForm(mapping, "")
		form["source_name"] = r.FormValue("source_name")
		form["payload"] = r.FormValue("payload")
		s.render(w, http.StatusUnprocessableEntity, "bank_mapping.html", pageData{
			Columns: parsed.Columns(mapping.HasHeader),
			Preview: preview,
			Error:   s.errText(err),
			Form:    form,
		})
		return
	}

	res, err := s.store.ImportBankRows(r.Context(), rows, r.FormValue("source_name"), skipped)
	if err != nil {
		log.Printf("import bank rows: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// New lines may need suggestions; the rules apply on the fly, the model
	// runs in the background (ADR 0020).
	s.startSuggestions()

	// A mapping that imported something is worth offering next time. Failing
	// to remember it is not worth failing the import over.
	if err := s.store.SaveBankMapping(r.Context(), parsed.Signature(mapping.HasHeader), mapping); err != nil {
		log.Printf("save bank mapping: %v", err)
	}

	s.renderBankWithResult(w, r, res)
}

func (s *Server) handleBankMatch(w http.ResponseWriter, r *http.Request) {
	txID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad transaction id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	var ids []int64
	for _, v := range r.Form["entry_id"] {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		s.bankError(w, r, s.T("Bitte eine Buchung auswählen."))
		return
	}

	err = s.store.MatchBankTransaction(r.Context(), txID, ids...)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case err != nil:
		// Mismatched amounts and directions are user-facing messages, not 500s.
		s.bankError(w, r, s.errText(err))
	default:
		http.Redirect(w, r, "/bank", http.StatusSeeOther)
	}
}

func (s *Server) handleBankUnmatch(w http.ResponseWriter, r *http.Request) {
	txID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad transaction id", http.StatusBadRequest)
		return
	}
	switch err := s.store.UnmatchBankTransaction(r.Context(), txID); {
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case err != nil:
		log.Printf("unmatch %d: %v", txID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	default:
		http.Redirect(w, r, "/bank", http.StatusSeeOther)
	}
}

// bankError re-renders the bank page carrying a message.
func (s *Server) bankError(w http.ResponseWriter, r *http.Request, msg string) {
	transactions, _ := s.store.ListBankTransactions(r.Context(), true, 200)
	candidates, _ := s.store.UnmatchedEntries(r.Context(), 500)
	_ = s.suggest(r.Context(), transactions, candidates)
	s.render(w, http.StatusUnprocessableEntity, "bank.html", pageData{
		Transactions: transactions, Entries: candidates,
		Error: msg, Form: map[string]string{},
	})
}

func (s *Server) renderBankWithResult(w http.ResponseWriter, r *http.Request, res store.ImportResult) {
	transactions, err := s.store.ListBankTransactions(r.Context(), true, 200)
	if err != nil {
		log.Printf("list bank transactions: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	candidates, _ := s.store.UnmatchedEntries(r.Context(), 500)
	_ = s.suggest(r.Context(), transactions, candidates)
	s.render(w, http.StatusOK, "bank.html", pageData{
		Transactions: transactions, Entries: candidates,
		Import: &res, Form: map[string]string{},
	})
}

// handleBankInvoice is "Rechnung bezahlt": it books the invoice's payment on
// the transaction's date and matches the resulting entries (one per VAT rate)
// to the transaction in one step (ADR 0014, ADR 0015).
func (s *Server) handleBankInvoice(w http.ResponseWriter, r *http.Request) {
	txID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || r.ParseForm() != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	invID, err := strconv.ParseInt(r.FormValue("invoice_id"), 10, 64)
	if err != nil {
		s.bankError(w, r, s.T("Bitte eine Rechnung auswählen."))
		return
	}
	tx, err := s.store.GetBankTransaction(r.Context(), txID)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "get bank transaction", err)
		return
	}
	inv, err := s.store.GetInvoice(r.Context(), invID)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "get invoice", err)
		return
	}
	if err := s.payInvoiceFromTransfer(r.Context(), tx, inv); err != nil {
		s.bankError(w, r, s.errText(err))
		return
	}
	http.Redirect(w, r, "/bank", http.StatusSeeOther)
}

// payInvoiceFromTransfer books an invoice's payment on the transfer's date and
// matches the resulting entries to the transfer (ADR 0014, ADR 0015). It
// checks everything before booking, so a mismatch leaves no half-done work.
func (s *Server) payInvoiceFromTransfer(ctx context.Context, tx store.BankTransaction, inv store.Invoice) error {
	switch {
	case tx.IsMatched():
		return errors.New(s.T("Dieser Umsatz ist bereits zugeordnet."))
	case tx.Direction() != "income" || tx.AbsAmountCents() > inv.OpenCents():
		return errors.New(s.Tf("Betrag oder Richtung des Umsatzes passen nicht zur Rechnung %s.", inv.Number))
	case inv.IsPaid():
		return errors.New(s.Tf("Rechnung %s ist bereits bezahlt.", inv.Number))
	}
	ids, err := s.store.BookInvoicePayment(ctx, store.InvoicePayment{InvoiceID: inv.ID, PaidOn: tx.BookedOn, AmountCents: tx.AbsAmountCents()})
	if err != nil {
		return err
	}
	if err := s.store.MatchBankTransaction(ctx, tx.ID, ids...); err != nil {
		// The payment is booked. Only the link failed, which the user can
		// redo by hand, so say exactly that.
		log.Printf("match invoice payment %v to tx %d: %v", ids, tx.ID, err)
		return errors.New(s.Tf("Zahlung gebucht, aber die Zuordnung zum Umsatz ist fehlgeschlagen: %s", s.errText(err)))
	}
	return nil
}

// sureInvoicePairs finds the transfers that name exactly one open invoice
// with the exact amount, where no other transfer claims that invoice. Only
// these qualify for the bulk action: an ambiguous case is left to the user.
func sureInvoicePairs(txs []store.BankTransaction) map[int64]int64 {
	txToInv := map[int64]int64{}
	claims := map[int64]int{}
	for _, tx := range txs {
		if tx.IsMatched() {
			continue
		}
		var sure []int64
		for _, h := range tx.InvoiceSuggestions {
			if h.Sure() {
				sure = append(sure, h.ID)
			}
		}
		if len(sure) == 1 {
			txToInv[tx.ID] = sure[0]
			claims[sure[0]]++
		}
	}
	for txID, invID := range txToInv {
		if claims[invID] > 1 {
			delete(txToInv, txID)
		}
	}
	return txToInv
}

// handleBankAutoMatch is "Alle eindeutigen zuordnen": every transfer that
// names its invoice and pays exactly the open amount is booked and matched.
// Nothing ambiguous is touched.
func (s *Server) handleBankAutoMatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	txs, err := s.store.ListBankTransactions(ctx, true, 500)
	if err != nil {
		s.fail(w, "list bank transactions", err)
		return
	}
	if err := s.suggest(ctx, txs, nil); err != nil {
		s.fail(w, "bank suggestions", err)
		return
	}
	pairs := sureInvoicePairs(txs)
	done := 0
	var failed []string
	for _, tx := range txs {
		invID, ok := pairs[tx.ID]
		if !ok {
			continue
		}
		inv, err := s.store.GetInvoice(ctx, invID)
		if err == nil {
			err = s.payInvoiceFromTransfer(ctx, tx, inv)
		}
		if err != nil {
			failed = append(failed, s.errText(err))
			continue
		}
		done++
	}
	if len(failed) > 0 {
		s.bankError(w, r, s.Tf("%d zugeordnet, %d nicht: %s", done, len(failed), strings.Join(failed, " ")))
		return
	}
	http.Redirect(w, r, "/bank?auto="+strconv.Itoa(done), http.StatusSeeOther)
}
