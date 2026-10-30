package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chrthal/accountant/internal/money"
)

// Entry is one booking. Entries are append-only (ADR 0002, ADR 0007): there is
// no update and no delete, and the database enforces that with triggers. A
// correction is a new entry that reverses an existing one.
type Entry struct {
	ID               int64
	BookedOn         string // 'YYYY-MM-DD'
	Direction        string // income | outgoing
	Description      string
	AmountGrossCents int64
	VATRate          int
	VATCents         int64
	NetCents         int64
	Konto            string
	Gegenkonto       string
	ContactID        *int64
	CategoryID       *int64
	ReversesEntryID  *int64
	InvoiceID        *int64 // set on a payment booked from an invoice (ADR 0015)
	// BusinessPct is the business share of an expense also used privately
	// (ADR 0041); 100 for all others.
	BusinessPct  int
	BusinessNote string // how the share was found
	CreatedAt    time.Time

	// Joined for display, not stored on the row.
	ContactName   string
	CategoryName  string
	InvoiceNumber string
	ReversedByID  *int64
	Attachments   int // number of Belege (ADR 0016)
}

// IsReversal reports whether this entry corrects another one.
func (e Entry) IsReversal() bool { return e.ReversesEntryID != nil }

// IsReversed reports whether a correcting entry exists for this one.
func (e Entry) IsReversed() bool { return e.ReversedByID != nil }

// Effective reports whether the entry counts towards totals: a booking that has
// been corrected drops out along with its correction, so the pair nets to zero
// without distorting either side's figures.
func (e Entry) Effective() bool { return !e.IsReversal() && !e.IsReversed() }

// IsMixed reports whether the expense is used partly privately.
func (e Entry) IsMixed() bool { return e.BusinessPct > 0 && e.BusinessPct < 100 }

// PrivatePct is the private share in percent.
func (e Entry) PrivatePct() int { return 100 - e.BusinessPct }

// BusinessVATCents is the part of the entry's VAT that counts as Vorsteuer
// by its business share, before the Kleinunternehmer rule.
func (e Entry) BusinessVATCents() int64 { return BusinessVAT(e.VATCents, e.BusinessPct) }

// MinBusinessPctForVAT is the business use below which a purchase does not
// count as made for the business, so none of its VAT is Vorsteuer (§ 12
// Abs 2 Z 1 lit a UStG).
const MinBusinessPctForVAT = 10

// Share is pct percent of cents, rounded half up. The SQL in shareSQL
// rounds the same way.
func Share(cents int64, pct int) int64 { return (cents*int64(pct) + 50) / 100 }

// BusinessVAT is the Vorsteuer part of vat at a business share of pct.
func BusinessVAT(vat int64, pct int) int64 {
	if pct < MinBusinessPctForVAT {
		return 0
	}
	return Share(vat, pct)
}

// currentPct and currentNote are an entry's business share as it counts:
// the newest change (ADR 0045), else the one it was booked with. The query
// must name the entry e.
const (
	currentPct = `COALESCE((SELECT s.business_pct FROM entry_shares s
		WHERE s.entry_id = e.id ORDER BY s.id DESC LIMIT 1), e.business_pct)`
	currentNote = `COALESCE((SELECT s.business_note FROM entry_shares s
		WHERE s.entry_id = e.id ORDER BY s.id DESC LIMIT 1), e.business_note)`
)

func shareSQL(col string) string { return "((" + col + " * " + currentPct + " + 50) / 100)" }

// businessVATSQL is the SQL of BusinessVAT for an entry e.
const businessVATSQL = "(CASE WHEN " + currentPct + " < 10 THEN 0 ELSE ((e.vat_cents * " + currentPct + " + 50) / 100) END)"

// NewEntry is the input for CreateEntry. VAT and net are not accepted from the
// caller — they are derived here so every stored booking is split the same way.
type NewEntry struct {
	BookedOn         string
	Direction        string
	Description      string
	AmountGrossCents int64
	VATRate          int
	Konto            string
	Gegenkonto       string
	ContactID        *int64
	CategoryID       *int64
	// BusinessPct is the business share of an expense (ADR 0041); 0 means 100.
	BusinessPct  int
	BusinessNote string

	// invoiceID is set only by BookInvoicePayment, never from a form.
	invoiceID *int64
}

// ErrAlreadyReversed is returned when an entry already has a correction.
var ErrAlreadyReversed = errors.New("entry has already been reversed")

// ErrNotFound is returned when an entry does not exist.
var ErrNotFound = errors.New("entry not found")

// Validate checks an entry before it reaches the database. The database has the
// same constraints — this exists to produce messages a person can act on.
func (n NewEntry) Validate() error {
	if _, err := time.Parse("2006-01-02", n.BookedOn); err != nil {
		return fmt.Errorf("booking date must be YYYY-MM-DD")
	}
	if n.Direction != "income" && n.Direction != "outgoing" {
		return fmt.Errorf("direction must be income or outgoing")
	}
	if strings.TrimSpace(n.Description) == "" {
		return fmt.Errorf("description is required")
	}
	if n.AmountGrossCents <= 0 {
		return fmt.Errorf("amount must be greater than zero")
	}
	if !money.IsValidVATRate(n.VATRate) {
		return fmt.Errorf("VAT rate must be one of 0, 10, 13, 20")
	}
	if n.BusinessPct < 0 || n.BusinessPct > 100 {
		return fmt.Errorf("the business share must be between 1 and 100 percent")
	}
	if n.Direction == "income" && n.BusinessPct != 0 && n.BusinessPct != 100 {
		return fmt.Errorf("only an expense can have a private share")
	}
	// Konto/Gegenkonto are deliberately not checked here (ADR 0012). They are
	// optional on a booking, resolved from the category where one supplies
	// them, and required only by the DATEV export, which checks them itself.
	return nil
}

const entrySelect = `
	SELECT e.id, e.booked_on, e.direction, `+currentDescription+`, e.amount_gross_cents,
	       e.vat_rate, e.vat_cents, e.net_cents, e.konto, e.gegenkonto,
	       e.contact_id, e.category_id, e.reverses_entry_id, e.invoice_id, `+currentPct+`, `+currentNote+`, e.created_at,
	       COALESCE(c.name, ''), COALESCE(g.name, ''), COALESCE(i.number, ''), r.id,
	       (SELECT COUNT(*) FROM attachments a WHERE a.entry_id = e.id)
	FROM entries e
	LEFT JOIN contacts   c ON c.id = e.contact_id
	LEFT JOIN categories g ON g.id = e.category_id
	LEFT JOIN invoices   i ON i.id = e.invoice_id
	LEFT JOIN entries    r ON r.reverses_entry_id = e.id`

func scanEntries(rows *sql.Rows) ([]Entry, error) {
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var (
			e                               Entry
			contactID, categoryID, reverses sql.NullInt64
			invoiceID, reversedBy           sql.NullInt64
		)
		if err := rows.Scan(&e.ID, &e.BookedOn, &e.Direction, &e.Description, &e.AmountGrossCents,
			&e.VATRate, &e.VATCents, &e.NetCents, &e.Konto, &e.Gegenkonto,
			&contactID, &categoryID, &reverses, &invoiceID, &e.BusinessPct, &e.BusinessNote, &e.CreatedAt,
			&e.ContactName, &e.CategoryName, &e.InvoiceNumber, &reversedBy, &e.Attachments); err != nil {
			return nil, err
		}
		if contactID.Valid {
			e.ContactID = &contactID.Int64
		}
		if categoryID.Valid {
			e.CategoryID = &categoryID.Int64
		}
		if reverses.Valid {
			e.ReversesEntryID = &reverses.Int64
		}
		if invoiceID.Valid {
			e.InvoiceID = &invoiceID.Int64
		}
		if reversedBy.Valid {
			e.ReversedByID = &reversedBy.Int64
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CreateEntry validates, resolves the account tags, splits the VAT, and
// appends a booking.
func (s *Store) CreateEntry(ctx context.Context, n NewEntry) (int64, error) {
	if err := n.Validate(); err != nil {
		return 0, err
	}
	if err := s.resolveAccounts(ctx, &n); err != nil {
		return 0, err
	}
	vat, net := money.SplitVAT(n.AmountGrossCents, n.VATRate)
	return s.insertEntry(ctx, s.DB, n, vat, net, nil)
}

// resolveAccounts fills a blank Konto/Gegenkonto from the booking's category
// (ADR 0012). The resolved values are then *stored on the row*: a booking's
// account tags must not move because a category was edited afterwards, which is
// the same rule the stored VAT split follows.
//
// A booking with no category, or a category with no defaults, simply keeps its
// blanks. Nothing in the ledger needs them — only the DATEV export does, and it
// refuses to run over blanks rather than inventing them.
func (s *Store) resolveAccounts(ctx context.Context, n *NewEntry) error {
	if n.CategoryID == nil {
		return nil
	}
	if strings.TrimSpace(n.Konto) != "" && strings.TrimSpace(n.Gegenkonto) != "" {
		return nil
	}
	c, err := s.GetCategory(ctx, *n.CategoryID)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("category does not exist")
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(n.Konto) == "" {
		n.Konto = c.DefaultKonto
	}
	if strings.TrimSpace(n.Gegenkonto) == "" {
		n.Gegenkonto = c.DefaultGegenkonto
	}
	return nil
}

// querier is what insertEntry needs: the pool or a transaction.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) insertEntry(ctx context.Context, q querier, n NewEntry, vat, net int64, reverses *int64) (int64, error) {
	stmt := s.Rebind(`INSERT INTO entries
		(booked_on, direction, description, amount_gross_cents, vat_rate, vat_cents,
		 net_cents, konto, gegenkonto, contact_id, category_id, reverses_entry_id, invoice_id,
		 business_pct, business_note, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`)
	pct := n.BusinessPct
	if pct == 0 {
		pct = 100
	}
	note := strings.TrimSpace(n.BusinessNote)
	if pct == 100 {
		note = ""
	}
	var id int64
	err := q.QueryRowContext(ctx, stmt,
		n.BookedOn, n.Direction, strings.TrimSpace(n.Description), n.AmountGrossCents,
		n.VATRate, vat, net, strings.TrimSpace(n.Konto), strings.TrimSpace(n.Gegenkonto),
		n.ContactID, n.CategoryID, reverses, n.invoiceID, pct, note, time.Now().UTC(),
	).Scan(&id)
	return id, err
}

// GetEntry returns one entry by id.
func (s *Store) GetEntry(ctx context.Context, id int64) (Entry, error) {
	rows, err := s.DB.QueryContext(ctx, s.Rebind(entrySelect+" WHERE e.id = ?"), id)
	if err != nil {
		return Entry{}, err
	}
	entries, err := scanEntries(rows)
	if err != nil {
		return Entry{}, err
	}
	if len(entries) == 0 {
		return Entry{}, ErrNotFound
	}
	return entries[0], nil
}

// ListEntries returns the most recent entries, newest booking date first.
func (s *Store) ListEntries(ctx context.Context, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx,
		s.Rebind(entrySelect+" ORDER BY e.booked_on DESC, e.id DESC LIMIT ?"), limit)
	if err != nil {
		return nil, err
	}
	return scanEntries(rows)
}

// ReverseEntry appends the correcting counter-booking for id: same amounts, the
// opposite direction, Konto and Gegenkonto swapped. The original is not touched
// — it cannot be (ADR 0007).
func (s *Store) ReverseEntry(ctx context.Context, id int64, bookedOn string) (int64, error) {
	orig, err := s.GetEntry(ctx, id)
	if err != nil {
		return 0, err
	}
	if orig.IsReversed() {
		return 0, ErrAlreadyReversed
	}
	if _, err := time.Parse("2006-01-02", bookedOn); err != nil {
		return 0, fmt.Errorf("booking date must be YYYY-MM-DD")
	}
	return s.insertReversal(ctx, s.DB, orig, bookedOn)
}

// insertReversal writes the Storno of orig on bookedOn, through the pool or
// inside a transaction.
func (s *Store) insertReversal(ctx context.Context, q querier, orig Entry, bookedOn string) (int64, error) {
	opposite := "outgoing"
	if orig.Direction == "outgoing" {
		opposite = "income"
	}

	n := NewEntry{
		BookedOn:         bookedOn,
		Direction:        opposite,
		Description:      "Storno zu #" + fmt.Sprint(orig.ID) + ": " + orig.Description,
		AmountGrossCents: orig.AmountGrossCents,
		VATRate:          orig.VATRate,
		Konto:            orig.Gegenkonto,
		Gegenkonto:       orig.Konto,
		ContactID:        orig.ContactID,
		CategoryID:       orig.CategoryID,
		BusinessPct:      orig.BusinessPct,
		BusinessNote:     orig.BusinessNote,
	}
	// The reversal reuses the original's stored split rather than recomputing
	// it, so the pair cancels to the cent even if the rounding rule ever moves.
	// The Storno does not carry the invoice link: it is the payment's reversal,
	// not a second payment, and leaving the link off keeps "paid" a plain sum
	// over effective payment entries.
	return s.insertEntry(ctx, q, n, orig.VATCents, orig.NetCents, &orig.ID)
}

// Totals sums the effective entries — those neither reversing nor reversed —
// for a calendar year. Gross figures are what the Kleinunternehmer threshold is
// measured against (§ 6 Abs 1 Z 27 UStG); the threshold logic itself is Phase 4.
type Totals struct {
	Year               int
	IncomeGrossCents   int64
	OutgoingGrossCents int64
	IncomeVATCents     int64
	OutgoingVATCents   int64 // all VAT paid on expenses, deductible or not
	// OutgoingVATBlockedCents is the part of OutgoingVATCents paid while the
	// Kleinunternehmerregelung applied: recorded, never deductible (ADR 0022).
	OutgoingVATBlockedCents int64
	// OutgoingVATPrivateCents is the VAT on the private share of mixed
	// expenses outside the Kleinunternehmer time: not Vorsteuer (ADR 0041).
	OutgoingVATPrivateCents int64
	// PrivateShareCents is the private share of mixed expenses, gross. It
	// is not a cost.
	PrivateShareCents int64
	IncomeNetCents    int64
	// OutgoingCostCents is what expenses cost the business for the profit:
	// net where the VAT is deductible, gross where it is not, times the
	// category's deductible share; assets are left out (ADR 0037).
	OutgoingCostCents int64
	// NonDeductibleCents is the share of expenses that does not count, e.g.
	// the half of a Bewirtung.
	NonDeductibleCents int64
	// DepreciationCents is the year's AfA over all assets.
	DepreciationCents int64
}

// ResultCents is income minus outgoings, gross — the cash-basis result.
func (t Totals) ResultCents() int64 { return t.IncomeGrossCents - t.OutgoingGrossCents }

// OutgoingVATDeductibleCents is the Vorsteuer that counts against the VAT owed.
func (t Totals) OutgoingVATDeductibleCents() int64 {
	return t.OutgoingVATCents - t.OutgoingVATBlockedCents - t.OutgoingVATPrivateCents
}

// PayableCents is the year's VAT owed minus deductible Vorsteuer: positive a
// Zahllast, negative a Gutschrift. Exempt periods add nothing on either side.
func (t Totals) PayableCents() int64 { return t.IncomeVATCents - t.OutgoingVATDeductibleCents() }

// ProfitCents approximates the profit of the Einnahmen-Ausgaben-Rechnung:
// income net of VAT, minus the deductible cost of expenses, minus AfA. It
// ignores what the books do not know, such as the Gewinnfreibetrag.
func (t Totals) ProfitCents() int64 {
	return t.IncomeNetCents - t.OutgoingCostCents - t.DepreciationCents
}

// costSQL is an entry's cost for the profit (ADR 0037, ADR 0041): net, or
// gross where the VAT was not deductible (ku), times the booking's business
// share and the category's deductible share, rounded half up once; 0 for
// income and for assets, whose cost goes in by AfA. full leaves the
// category's share out. The query must join categories as g.
func costSQL(ku string, full bool) string {
	share := "COALESCE(g.deductible_pct, 100)"
	if full {
		share = "100"
	}
	return `CASE WHEN e.direction = 'outgoing' AND NOT EXISTS (SELECT 1 FROM assets a WHERE a.entry_id = e.id)
		THEN ((CASE WHEN ` + ku + ` THEN e.amount_gross_cents ELSE e.net_cents END) * ` + currentPct + ` * ` + share + ` + 5000) / 10000
		ELSE 0 END`
}

// Totals returns the yearly figures over effective entries only.
func (s *Store) Totals(ctx context.Context, year int) (Totals, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return Totals{}, err
	}
	ku, kuArgs := settings.kuSQL("e.booked_on")
	q := `
		SELECT
		  COALESCE(SUM(CASE WHEN e.direction = 'income'   THEN e.amount_gross_cents ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN e.direction = 'outgoing' THEN e.amount_gross_cents ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN e.direction = 'income'   THEN e.vat_cents ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN e.direction = 'outgoing' THEN e.vat_cents ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN e.direction = 'outgoing' AND ` + ku + ` THEN e.vat_cents ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN e.direction = 'outgoing' AND NOT (` + ku + `) THEN e.vat_cents - ` + businessVATSQL + ` ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN e.direction = 'outgoing' THEN e.amount_gross_cents - ` + shareSQL("e.amount_gross_cents") + ` ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN e.direction = 'income'   THEN e.net_cents ELSE 0 END), 0),
		  COALESCE(SUM(` + costSQL(ku, false) + `), 0),
		  COALESCE(SUM(` + costSQL(ku, true) + `), 0)
		FROM entries e
		LEFT JOIN categories g ON g.id = e.category_id
		WHERE e.booked_on >= ? AND e.booked_on <= ?
		  AND e.reverses_entry_id IS NULL
		  AND NOT EXISTS (SELECT 1 FROM entries r WHERE r.reverses_entry_id = e.id)`

	t := Totals{Year: year}
	args := append(append(append(append(append([]any{}, kuArgs...), kuArgs...), kuArgs...), kuArgs...),
		fmt.Sprintf("%04d-01-01", year), fmt.Sprintf("%04d-12-31", year))
	var fullCost int64
	if err := s.DB.QueryRowContext(ctx, s.Rebind(q), args...).Scan(
		&t.IncomeGrossCents, &t.OutgoingGrossCents, &t.IncomeVATCents, &t.OutgoingVATCents,
		&t.OutgoingVATBlockedCents, &t.OutgoingVATPrivateCents, &t.PrivateShareCents, &t.IncomeNetCents, &t.OutgoingCostCents, &fullCost); err != nil {
		return t, err
	}
	t.NonDeductibleCents = fullCost - t.OutgoingCostCents
	t.DepreciationCents, err = s.DepreciationFor(ctx, year)
	return t, err
}

// MonthTotals is one calendar month of effective bookings, gross.
type MonthTotals struct {
	Month              int // 1–12
	IncomeGrossCents   int64
	OutgoingGrossCents int64
}

// MonthlyTotals returns all twelve months of a year, months without bookings
// included as zeros, over effective entries only — the same rule as Totals, so
// the chart and the headline figures can never disagree.
func (s *Store) MonthlyTotals(ctx context.Context, year int) ([12]MonthTotals, error) {
	const q = `
		SELECT SUBSTR(e.booked_on, 6, 2),
		  COALESCE(SUM(CASE WHEN e.direction = 'income'   THEN e.amount_gross_cents ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN e.direction = 'outgoing' THEN e.amount_gross_cents ELSE 0 END), 0)
		FROM entries e
		WHERE e.booked_on >= ? AND e.booked_on <= ?
		  AND e.reverses_entry_id IS NULL
		  AND NOT EXISTS (SELECT 1 FROM entries r WHERE r.reverses_entry_id = e.id)
		GROUP BY SUBSTR(e.booked_on, 6, 2)`

	var out [12]MonthTotals
	for i := range out {
		out[i].Month = i + 1
	}
	rows, err := s.DB.QueryContext(ctx, s.Rebind(q),
		fmt.Sprintf("%04d-01-01", year), fmt.Sprintf("%04d-12-31", year))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			mm       string
			in, outg int64
		)
		if err := rows.Scan(&mm, &in, &outg); err != nil {
			return out, err
		}
		var m int
		if _, err := fmt.Sscanf(mm, "%d", &m); err != nil || m < 1 || m > 12 {
			return out, fmt.Errorf("unexpected month %q in booked_on", mm)
		}
		out[m-1].IncomeGrossCents = in
		out[m-1].OutgoingGrossCents = outg
	}
	return out, rows.Err()
}

// EntryFilter narrows the booking list. Zero values mean "no restriction".
type EntryFilter struct {
	Year       int
	Month      int // 1–12, only together with Year
	Direction  string
	CategoryID int64
	Query      string // matched against description and contact name
	// WithoutReceipt keeps only effective outgoing bookings without a Beleg.
	WithoutReceipt bool
	Limit          int
	Offset         int
}

// EntryPage is one page of a filtered list, with the totals of the whole
// filtered set rather than of the page, so the sums do not change as you page.
type EntryPage struct {
	Entries            []Entry
	Total              int
	IncomeGrossCents   int64 // effective entries only, like Totals
	OutgoingGrossCents int64
}

// FindEntries lists bookings matching f, newest first.
func (s *Store) FindEntries(ctx context.Context, f EntryFilter) (EntryPage, error) {
	var (
		where []string
		args  []any
	)
	switch {
	case f.Year > 0 && f.Month >= 1 && f.Month <= 12:
		where = append(where, "e.booked_on >= ? AND e.booked_on <= ?")
		args = append(args, fmt.Sprintf("%04d-%02d-01", f.Year, f.Month), fmt.Sprintf("%04d-%02d-31", f.Year, f.Month))
	case f.Year > 0:
		where = append(where, "e.booked_on >= ? AND e.booked_on <= ?")
		args = append(args, fmt.Sprintf("%04d-01-01", f.Year), fmt.Sprintf("%04d-12-31", f.Year))
	}
	if f.Direction == "income" || f.Direction == "outgoing" {
		where = append(where, "e.direction = ?")
		args = append(args, f.Direction)
	}
	if f.CategoryID > 0 {
		where = append(where, "e.category_id = ?")
		args = append(args, f.CategoryID)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		// LOWER on both sides: SQLite's LIKE is ASCII-case-insensitive only
		// and Postgres's is case-sensitive, so neither can be relied on alone.
		// Umlauts still compare case-sensitively on SQLite, which is the dev
		// engine; Postgres lowercases them properly.
		where = append(where, "(LOWER("+currentDescription+") LIKE ? OR LOWER(COALESCE(c.name, '')) LIKE ?)")
		like := "%" + strings.ToLower(escapeLike(q)) + "%"
		args = append(args, like, like)
	}
	if f.WithoutReceipt {
		where = append(where, `e.direction = 'outgoing' AND e.reverses_entry_id IS NULL
			AND NOT EXISTS (SELECT 1 FROM entries rr WHERE rr.reverses_entry_id = e.id)
			AND NOT EXISTS (SELECT 1 FROM attachments a WHERE a.entry_id = e.id)`)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}

	var page EntryPage
	sumQ := `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN e.direction = 'income' AND e.reverses_entry_id IS NULL AND r.id IS NULL
		                  THEN e.amount_gross_cents ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN e.direction = 'outgoing' AND e.reverses_entry_id IS NULL AND r.id IS NULL
		                  THEN e.amount_gross_cents ELSE 0 END), 0)
		FROM entries e
		LEFT JOIN contacts c ON c.id = e.contact_id
		LEFT JOIN entries  r ON r.reverses_entry_id = e.id` + cond
	if err := s.DB.QueryRowContext(ctx, s.Rebind(sumQ), args...).Scan(
		&page.Total, &page.IncomeGrossCents, &page.OutgoingGrossCents); err != nil {
		return page, err
	}

	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	offset := max(f.Offset, 0)
	rows, err := s.DB.QueryContext(ctx,
		s.Rebind(entrySelect+cond+" ORDER BY e.booked_on DESC, e.id DESC LIMIT ? OFFSET ?"),
		append(args, limit, offset)...)
	if err != nil {
		return page, err
	}
	page.Entries, err = scanEntries(rows)
	return page, err
}

// escapeLike neutralises LIKE wildcards in user input. The engines disagree
// on escaping (Postgres escapes with a backslash by default, SQLite only with
// an explicit ESCAPE clause), so the wildcards are dropped instead. Nobody
// needs to search for a literal "%" in a booking text.
func escapeLike(s string) string {
	return strings.NewReplacer("%", "", "_", "").Replace(s)
}

// BookingYears returns the years that have bookings, newest first, always
// including the current year so an empty ledger still has one to show.
func (s *Store) BookingYears(ctx context.Context) ([]int, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT DISTINCT SUBSTR(booked_on, 1, 4) FROM entries ORDER BY 1 DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now().Year()
	out := []int{}
	seen := map[int]bool{}
	for rows.Next() {
		var y string
		if err := rows.Scan(&y); err != nil {
			return nil, err
		}
		var n int
		if _, err := fmt.Sscanf(y, "%d", &n); err == nil && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if !seen[now] {
		out = append([]int{now}, out...)
	}
	return out, rows.Err()
}

// RetentionYears is how many years back Belege and books must be kept
// (§ 132 BAO), and so how far back the exports are offered.
const RetentionYears = 7

// ExportYears returns the years an export can be made for, newest first:
// the current year and the RetentionYears before it, and any other year that
// holds bookings, invoices or bank transactions.
func (s *Store) ExportYears(ctx context.Context) (years []int, withData map[int]bool, err error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT SUBSTR(booked_on, 1, 4) FROM entries
		UNION SELECT SUBSTR(issue_date, 1, 4) FROM invoices
		UNION SELECT SUBSTR(booked_on, 1, 4) FROM bank_transactions`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	now := time.Now().Year()
	lo, hi := now-RetentionYears, now
	withData = map[int]bool{}
	for rows.Next() {
		var y string
		if err := rows.Scan(&y); err != nil {
			return nil, nil, err
		}
		var n int
		if _, err := fmt.Sscanf(y, "%d", &n); err == nil {
			withData[n] = true
			lo, hi = min(lo, n), max(hi, n)
		}
	}
	for y := hi; y >= lo; y-- {
		years = append(years, y)
	}
	return years, withData, rows.Err()
}
