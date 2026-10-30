package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/chrthal/accountant/internal/bankcsv"
	"github.com/chrthal/accountant/internal/match"
)

// BankTransaction is one imported bank movement. What the bank sent never
// changes. Only its links to bookings do (bank_matches), because reconciling
// is housekeeping rather than a change to the books (ADR 0009, ADR 0014). The
// one-time split of labelled text keeps the original in RawText (ADR 0020).
type BankTransaction struct {
	ID               int64
	BookedOn         string
	AmountCents      int64 // signed: negative left the account
	Counterparty     string
	CounterpartyIBAN string
	Purpose          string
	RawText          string // the bank's text, kept when Counterparty was split out of it
	RowHash          string
	SourceName       string
	ImportedAt       time.Time
	// PrivateAt is set when the line was marked private: a transfer between
	// the business and its owner, outside the books (ADR 0035).
	PrivateAt *time.Time

	// The bookings this transaction is matched to (ADR 0014): usually one,
	// several when one transfer pays several bookings, e.g. a mixed-rate
	// invoice. Loaded by the list and get queries.
	Matches []Entry

	// Suggestions is filled by the server for display, never stored.
	Suggestions []Entry
	// OwnTransfer: the counterparty is the business owner, so the line is
	// likely private. Filled by the server for display.
	OwnTransfer        bool
	InvoiceSuggestions []match.InvoiceHint
}

// Direction reports which way the money went, in the ledger's vocabulary.
func (b BankTransaction) Direction() string {
	if b.AmountCents < 0 {
		return "outgoing"
	}
	return "income"
}

// AbsAmountCents is the amount without its sign, which is how entries store it.
func (b BankTransaction) AbsAmountCents() int64 {
	if b.AmountCents < 0 {
		return -b.AmountCents
	}
	return b.AmountCents
}

// IsMatched reports whether this transaction is linked to any ledger entry.
func (b BankTransaction) IsMatched() bool { return len(b.Matches) > 0 }

// IsPrivate reports whether the line was marked a private transfer.
func (b BankTransaction) IsPrivate() bool { return b.PrivateAt != nil }

// PrivateKind names a private line as the books do: money taken out is a
// Privatentnahme, money put in a Privateinlage.
func (b BankTransaction) PrivateKind() string {
	if b.AmountCents < 0 {
		return "Privatentnahme"
	}
	return "Privateinlage"
}

// MatchedEntryDescription names what the transaction is matched to.
func (b BankTransaction) MatchedEntryDescription() string {
	if len(b.Matches) == 0 {
		return ""
	}
	if len(b.Matches) == 1 {
		return b.Matches[0].Description
	}
	if b.Matches[0].InvoiceNumber != "" {
		return fmt.Sprintf("Rechnung %s (%d Buchungen)", b.Matches[0].InvoiceNumber, len(b.Matches))
	}
	return fmt.Sprintf("%d Buchungen", len(b.Matches))
}

// ErrAlreadyMatched is returned when a transaction or entry is already linked.
var ErrAlreadyMatched = errors.New("already matched")

// ImportResult reports what an import did. Duplicates are expected, not
// failures: people re-export overlapping date ranges routinely.
type ImportResult struct {
	Imported   int
	Duplicates int
	Skipped    []string
}

// ImportBankRows stores parsed rows, ignoring any whose hash is already known.
func (s *Store) ImportBankRows(ctx context.Context, rows []bankcsv.Row, sourceName string, skipped []string) (ImportResult, error) {
	res := ImportResult{Skipped: skipped}
	now := time.Now().UTC()

	for _, r := range rows {
		var exists int
		if err := s.DB.QueryRowContext(ctx,
			s.Rebind("SELECT COUNT(*) FROM bank_transactions WHERE row_hash = ?"), r.Hash).Scan(&exists); err != nil {
			return res, err
		}
		if exists > 0 {
			res.Duplicates++
			continue
		}

		if _, err := s.DB.ExecContext(ctx, s.Rebind(`INSERT INTO bank_transactions
			(booked_on, amount_cents, counterparty, counterparty_iban, purpose, raw_text,
			 row_hash, source_name, imported_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`),
			r.BookedOn, r.AmountCents, r.Counterparty, r.CounterpartyIBAN, r.Purpose, r.RawText,
			r.Hash, sourceName, now); err != nil {
			// A concurrent import of the same file is a duplicate, not an error.
			if isUniqueViolation(err) {
				res.Duplicates++
				continue
			}
			return res, err
		}
		res.Imported++
	}
	return res, nil
}

const bankSelect = `
	SELECT b.id, b.booked_on, b.amount_cents, b.counterparty, b.counterparty_iban, b.purpose,
	       b.raw_text, b.row_hash, b.source_name, b.imported_at, b.private_at
	FROM bank_transactions b`

// openBank selects the lines that still need something done: neither matched
// to a booking nor marked private.
const openBank = ` NOT EXISTS (SELECT 1 FROM bank_matches m WHERE m.bank_transaction_id = b.id) AND b.private_at IS NULL`

func (s *Store) scanBankRows(ctx context.Context, rows *sql.Rows) ([]BankTransaction, error) {
	var out []BankTransaction
	for rows.Next() {
		var (
			b       BankTransaction
			private sql.NullTime
		)
		if err := rows.Scan(&b.ID, &b.BookedOn, &b.AmountCents, &b.Counterparty, &b.CounterpartyIBAN,
			&b.Purpose, &b.RawText, &b.RowHash, &b.SourceName, &b.ImportedAt, &private); err != nil {
			rows.Close()
			return nil, err
		}
		if private.Valid {
			b.PrivateAt = &private.Time
		}
		out = append(out, b)
	}
	err := rows.Err()
	rows.Close() // before the next query: SQLite has a single connection
	if err != nil || len(out) == 0 {
		return out, err
	}
	return out, s.loadMatches(ctx, out)
}

// loadMatches attaches the matched entries to each transaction in one query.
func (s *Store) loadMatches(ctx context.Context, txs []BankTransaction) error {
	byID := make(map[int64]int, len(txs))
	ids := make([]string, 0, len(txs))
	for i, t := range txs {
		byID[t.ID] = i
		ids = append(ids, fmt.Sprint(t.ID))
	}
	// The ids are integers from our own query, so building the IN list as
	// text is safe; it also avoids one placeholder per row.
	rows, err := s.DB.QueryContext(ctx, `
		SELECT m.bank_transaction_id, e.id
		FROM bank_matches m JOIN entries e ON e.id = m.entry_id
		WHERE m.bank_transaction_id IN (`+strings.Join(ids, ",")+`)
		ORDER BY e.id`)
	if err != nil {
		return err
	}
	type link struct{ tx, entry int64 }
	var links []link
	for rows.Next() {
		var l link
		if err := rows.Scan(&l.tx, &l.entry); err != nil {
			rows.Close()
			return err
		}
		links = append(links, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, l := range links {
		e, err := s.GetEntry(ctx, l.entry)
		if err != nil {
			return err
		}
		i := byID[l.tx]
		txs[i].Matches = append(txs[i].Matches, e)
	}
	return nil
}

// ListBankTransactions returns transactions newest first. When onlyUnmatched is
// set, the ones already linked to an entry are left out: that is the working
// view while reconciling.
func (s *Store) ListBankTransactions(ctx context.Context, onlyUnmatched bool, limit int) ([]BankTransaction, error) {
	if limit <= 0 {
		limit = 200
	}
	q := bankSelect
	if onlyUnmatched {
		q += " WHERE" + openBank
	}
	q += " ORDER BY b.booked_on DESC, b.id DESC LIMIT ?"

	rows, err := s.DB.QueryContext(ctx, s.Rebind(q), limit)
	if err != nil {
		return nil, err
	}
	return s.scanBankRows(ctx, rows)
}

// GetBankTransaction returns one transaction.
func (s *Store) GetBankTransaction(ctx context.Context, id int64) (BankTransaction, error) {
	rows, err := s.DB.QueryContext(ctx, s.Rebind(bankSelect+" WHERE b.id = ?"), id)
	if err != nil {
		return BankTransaction{}, err
	}
	list, err := s.scanBankRows(ctx, rows)
	if err != nil {
		return BankTransaction{}, err
	}
	if len(list) == 0 {
		return BankTransaction{}, ErrNotFound
	}
	return list[0], nil
}

// MatchBankTransaction links a transaction to one or more ledger entries.
//
// Each entry can be matched once (UNIQUE entry_id): an entry is one booking of
// money that moved once. A transaction can carry several entries when one
// transfer covers several bookings (ADR 0014). Then their sum must equal the
// transaction, and every one must go the same direction. A mismatch is refused:
// silently linking a €10 transaction to a €1.000 booking would hide an error
// rather than surface it.
func (s *Store) MatchBankTransaction(ctx context.Context, txID int64, entryIDs ...int64) error {
	if len(entryIDs) == 0 {
		return fmt.Errorf("no booking selected")
	}
	tx, err := s.GetBankTransaction(ctx, txID)
	if err != nil {
		return err
	}
	if tx.IsPrivate() {
		return fmt.Errorf("this transaction is marked private; undo that first")
	}
	if tx.IsMatched() {
		return fmt.Errorf("%w: this transaction is already linked to a booking", ErrAlreadyMatched)
	}

	var sum int64
	seen := map[int64]bool{}
	for _, id := range entryIDs {
		if seen[id] {
			return fmt.Errorf("booking #%d is listed twice", id)
		}
		seen[id] = true
		entry, err := s.GetEntry(ctx, id)
		if err != nil {
			return err
		}
		var used int
		if err := s.DB.QueryRowContext(ctx,
			s.Rebind("SELECT COUNT(*) FROM bank_matches WHERE entry_id = ?"), id).Scan(&used); err != nil {
			return err
		}
		if used > 0 {
			return fmt.Errorf("%w: booking #%d is already linked to another transaction", ErrAlreadyMatched, id)
		}
		if entry.Direction != tx.Direction() {
			return fmt.Errorf("directions do not match: the transaction is %s, booking #%d is %s",
				tx.Direction(), id, entry.Direction)
		}
		sum += entry.AmountGrossCents
	}
	if sum != tx.AbsAmountCents() {
		return fmt.Errorf("amounts do not match: the transaction is %d cents, the booking(s) %d cents",
			tx.AbsAmountCents(), sum)
	}

	dbtx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer dbtx.Rollback()
	for _, id := range entryIDs {
		if _, err := dbtx.ExecContext(ctx,
			s.Rebind("INSERT INTO bank_matches (bank_transaction_id, entry_id) VALUES (?, ?)"), txID, id); err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("%w: a booking was matched concurrently", ErrAlreadyMatched)
			}
			return err
		}
	}
	return dbtx.Commit()
}

// UnmatchBankTransaction removes all links of a transaction. Matching is
// correctable, and the entries it pointed at are untouched either way.
func (s *Store) UnmatchBankTransaction(ctx context.Context, txID int64) error {
	if _, err := s.GetBankTransaction(ctx, txID); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx,
		s.Rebind("DELETE FROM bank_matches WHERE bank_transaction_id = ?"), txID)
	return err
}

// UnmatchedEntries returns effective bookings that no bank transaction points
// at. These are the candidates when matching a transaction. A reversed booking
// and its Storno are left out: together they net to zero, so no money
// moved for them to match.
func (s *Store) UnmatchedEntries(ctx context.Context, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 200
	}
	q := entrySelect + `
		WHERE NOT EXISTS (SELECT 1 FROM bank_matches m WHERE m.entry_id = e.id)
		  AND e.reverses_entry_id IS NULL AND r.id IS NULL
		ORDER BY e.booked_on DESC, e.id DESC LIMIT ?`
	rows, err := s.DB.QueryContext(ctx, s.Rebind(q), limit)
	if err != nil {
		return nil, err
	}
	return scanEntries(rows)
}

// CountUnmatchedBank is how many imported transactions still wait for a
// booking: the overview's reconciliation to-do.
func (s *Store) CountUnmatchedBank(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM bank_transactions b WHERE`+openBank).Scan(&n)
	return n, err
}

// BankTransactionsForEntry returns the transaction a booking is matched to.
func (s *Store) BankTransactionsForEntry(ctx context.Context, entryID int64) ([]BankTransaction, error) {
	rows, err := s.DB.QueryContext(ctx, s.Rebind(bankSelect+`
		WHERE b.id IN (SELECT bank_transaction_id FROM bank_matches WHERE entry_id = ?)`), entryID)
	if err != nil {
		return nil, err
	}
	return s.scanBankRows(ctx, rows)
}

// SuggestMatches picks, from candidates, the entries that could be this
// transaction: same direction and same amount, nearest date first, and at
// most five.
func SuggestMatches(tx BankTransaction, candidates []Entry) []Entry {
	var out []Entry
	for _, e := range candidates {
		if e.Direction == tx.Direction() && e.AmountGrossCents == tx.AbsAmountCents() {
			out = append(out, e)
		}
	}
	dist := func(e Entry) float64 {
		a, err1 := time.Parse("2006-01-02", e.BookedOn)
		b, err2 := time.Parse("2006-01-02", tx.BookedOn)
		if err1 != nil || err2 != nil {
			return 1e9
		}
		return math.Abs(a.Sub(b).Hours())
	}
	// A booking whose contact or text names the counterparty comes first,
	// then the nearest date.
	named := func(e Entry) bool {
		return tx.Counterparty != "" && (match.SimilarName(tx.Counterparty, e.ContactName) ||
			match.SimilarName(tx.Counterparty, e.Description))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ni, nj := named(out[i]), named(out[j]); ni != nj {
			return ni
		}
		return dist(out[i]) < dist(out[j])
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// SuggestInvoices ranks open invoices for an incoming transaction by the
// rules in package match: invoice number in the text, exact amount, sender
// name (ADR 0020). Outgoing transactions get none.
func SuggestInvoices(tx BankTransaction, open []Invoice) []match.InvoiceHint {
	if tx.Direction() != "income" {
		return nil
	}
	cands := make([]match.Invoice, 0, len(open))
	for _, inv := range open {
		// A paid invoice takes no more; a partly paid one takes the rest or
		// another part (ADR 0036).
		if inv.IsPaid() {
			continue
		}
		cands = append(cands, match.Invoice{ID: inv.ID, Number: inv.Number,
			RecipientName: inv.RecipientName, OpenCents: inv.OpenCents()})
	}
	return match.Invoices(tx.Purpose+" "+tx.RawText, tx.Counterparty, tx.AbsAmountCents(), cands)
}

// SavedBankMapping returns the mapping last confirmed for a column layout
// (bankcsv.File.Signature), if there is one.
func (s *Store) SavedBankMapping(ctx context.Context, signature string) (bankcsv.Mapping, bool, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx,
		s.Rebind(`SELECT value FROM settings WHERE "key" = ?`), "bank_mapping:"+signature).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return bankcsv.Mapping{}, false, nil
	}
	if err != nil {
		return bankcsv.Mapping{}, false, err
	}
	var m bankcsv.Mapping
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		// A mapping that no longer decodes is just forgotten; the guess takes over.
		return bankcsv.Mapping{}, false, nil
	}
	return m, true, nil
}

// SaveBankMapping remembers the mapping of a successful import, so the next
// export with the same layout opens with it preselected (plan 0001 noted this
// as the obvious next step).
func (s *Store) SaveBankMapping(ctx context.Context, signature string, m bankcsv.Mapping) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	key := "bank_mapping:" + signature
	if _, err := tx.ExecContext(ctx, s.Rebind(`DELETE FROM settings WHERE "key" = ?`), key); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.Rebind(`INSERT INTO settings ("key", value) VALUES (?, ?)`), key, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

// OpenInvoices returns invoices without an effective payment, oldest first.
func (s *Store) OpenInvoices(ctx context.Context) ([]Invoice, error) {
	all, err := s.ListInvoices(ctx, 1000)
	if err != nil {
		return nil, err
	}
	var out []Invoice
	for i := len(all) - 1; i >= 0; i-- {
		if !all[i].IsPaid() {
			out = append(out, all[i])
		}
	}
	return out, nil
}

// BackfillCounterparties splits the labelled text of transactions imported
// before the split existed (ADR 0020). It only touches rows without a
// counterparty whose text carries a counterparty label, keeps the original
// text in raw_text, and leaves the row hash alone, so re-importing the same
// export is still recognised as a duplicate. Running it again does nothing.
func (s *Store) BackfillCounterparties(ctx context.Context) (int, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, purpose FROM bank_transactions WHERE counterparty = '' AND raw_text = ''`)
	if err != nil {
		return 0, err
	}
	type fix struct {
		id  int64
		raw string
		l   bankcsv.Labelled
	}
	var fixes []fix
	for rows.Next() {
		var f fix
		if err := rows.Scan(&f.id, &f.raw); err != nil {
			rows.Close()
			return 0, err
		}
		if l, ok := bankcsv.SplitText(f.raw); ok {
			f.l = l
			fixes = append(fixes, f)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, f := range fixes {
		if _, err := s.DB.ExecContext(ctx, s.Rebind(`UPDATE bank_transactions
			SET counterparty = ?, counterparty_iban = ?, purpose = ?, raw_text = ?
			WHERE id = ? AND counterparty = '' AND raw_text = ''`),
			f.l.Counterparty, f.l.IBAN, f.l.Purpose, f.raw, f.id); err != nil {
			return 0, err
		}
	}
	return len(fixes), nil
}

// PreviousBooking finds the most recent effective booking matched to an
// earlier transaction from the same counterparty, by IBAN when the bank gave
// one, otherwise by name, and in the same direction. It is the rule behind
// "wie zuletzt" (ADR 0020): rent, phone and SVS look the same every month.
// Invoice payments are left out, since they are no template for anything.
func (s *Store) PreviousBooking(ctx context.Context, tx BankTransaction) (Entry, bool, error) {
	if tx.CounterpartyIBAN == "" && strings.TrimSpace(tx.Counterparty) == "" {
		return Entry{}, false, nil
	}
	q := entrySelect + `
		JOIN bank_matches m ON m.entry_id = e.id
		JOIN bank_transactions b ON b.id = m.bank_transaction_id
		WHERE b.id <> ?
		  AND ((? <> '' AND b.counterparty_iban = ?) OR (? <> '' AND LOWER(b.counterparty) = LOWER(?)))
		  AND e.direction = ? AND e.invoice_id IS NULL
		  AND e.reverses_entry_id IS NULL AND r.id IS NULL
		ORDER BY e.booked_on DESC, e.id DESC LIMIT 1`
	name := strings.TrimSpace(tx.Counterparty)
	rows, err := s.DB.QueryContext(ctx, s.Rebind(q), tx.ID,
		tx.CounterpartyIBAN, tx.CounterpartyIBAN, name, name, tx.Direction())
	if err != nil {
		return Entry{}, false, err
	}
	list, err := scanEntries(rows)
	if err != nil || len(list) == 0 {
		return Entry{}, false, err
	}
	return list[0], true, nil
}

// MarkPrivate marks a line as a private transfer (ADR 0035). A line matched
// to a booking cannot be private: the money either belongs to the books or
// it does not.
func (s *Store) MarkPrivate(ctx context.Context, txID int64) error {
	tx, err := s.GetBankTransaction(ctx, txID)
	if err != nil {
		return err
	}
	if tx.IsMatched() {
		return fmt.Errorf("this transaction is matched to a booking; undo the match first")
	}
	_, err = s.DB.ExecContext(ctx, s.Rebind(`UPDATE bank_transactions SET private_at = ? WHERE id = ? AND private_at IS NULL`),
		time.Now().UTC(), txID)
	return err
}

// UnmarkPrivate makes a private line open again.
func (s *Store) UnmarkPrivate(ctx context.Context, txID int64) error {
	_, err := s.DB.ExecContext(ctx, s.Rebind(`UPDATE bank_transactions SET private_at = NULL WHERE id = ?`), txID)
	return err
}

// PrivateTotals is what went between the business and its owner in a year.
type PrivateTotals struct {
	WithdrawalCents int64 // Privatentnahmen, as a positive amount
	DepositCents    int64 // Privateinlagen
	Lines           int
}

// PrivateTotalsFor sums the private lines booked in the year.
func (s *Store) PrivateTotalsFor(ctx context.Context, year int) (PrivateTotals, error) {
	var t PrivateTotals
	err := s.DB.QueryRowContext(ctx, s.Rebind(`SELECT
		COALESCE(SUM(CASE WHEN amount_cents < 0 THEN -amount_cents ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN amount_cents > 0 THEN amount_cents ELSE 0 END), 0),
		COUNT(*)
		FROM bank_transactions WHERE private_at IS NOT NULL AND booked_on >= ? AND booked_on <= ?`),
		fmt.Sprintf("%04d-01-01", year), fmt.Sprintf("%04d-12-31", year)).Scan(&t.WithdrawalCents, &t.DepositCents, &t.Lines)
	return t, err
}

// ConvertToPrivate turns a bank line that was booked as income or expense
// into the private transfer it is (ADR 0035): each matched booking is
// reversed by a Storno on its own date, the match is removed and the line is
// marked private, all in one transaction. A Storno on the original date keeps
// every year's figures right. Invoice payments and assets are refused: they
// belong to the books, and undoing them is a decision of its own.
// It returns how many bookings were reversed.
func (s *Store) ConvertToPrivate(ctx context.Context, txID int64) (int, error) {
	bt, err := s.GetBankTransaction(ctx, txID)
	if err != nil {
		return 0, err
	}
	if bt.IsPrivate() {
		return 0, nil
	}
	if !bt.IsMatched() {
		return 0, s.MarkPrivate(ctx, txID)
	}
	for _, e := range bt.Matches {
		switch {
		case e.InvoiceID != nil:
			return 0, fmt.Errorf("an invoice payment stays in the books and is not moved to private")
		case e.IsReversal() || e.IsReversed():
			return 0, fmt.Errorf("a matched booking is already reversed; undo the match first")
		}
		if _, isAsset, err := s.AssetForEntry(ctx, e.ID); err != nil {
			return 0, err
		} else if isAsset {
			return 0, fmt.Errorf("a matched booking is written off as an asset; remove the asset first")
		}
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, e := range bt.Matches {
		if _, err := s.insertReversal(ctx, tx, e, e.BookedOn); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, s.Rebind(`DELETE FROM bank_matches WHERE bank_transaction_id = ?`), txID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, s.Rebind(`UPDATE bank_transactions SET private_at = ? WHERE id = ?`), time.Now().UTC(), txID); err != nil {
		return 0, err
	}
	return len(bt.Matches), tx.Commit()
}
