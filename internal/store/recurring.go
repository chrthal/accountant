package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Recurring is a recurring invoice (ADR 0038): a template the app reminds
// about when its next invoice is due. It never sends or issues anything.
type Recurring struct {
	ID          int64
	ContactID   int64
	ContactName string
	Name        string // what it is, e.g. "Hosting"
	Interval    string // monthly, quarterly, yearly
	NextDue     string // 'YYYY-MM-DD'
	Lines       []DraftLine
	Note        string
	Active      bool
	CreatedAt   time.Time
	// The last invoice issued from it, if any.
	LastNumber string
	LastDate   string
}

// Intervals are the intervals a recurring invoice can have, in order.
var Intervals = []string{"monthly", "quarterly", "yearly"}

// Due reports whether the next invoice is due within the given days.
func (r Recurring) Due(today time.Time, days int) bool {
	d, err := time.Parse("2006-01-02", r.NextDue)
	return err == nil && r.Active && !d.After(today.AddDate(0, 0, days))
}

// Overdue reports whether the due date has passed.
func (r Recurring) Overdue(today time.Time) bool {
	return r.Active && r.NextDue < today.Format("2006-01-02")
}

// Period names the period the next invoice is for, to fill {Zeitraum} in
// the positions: "10/2026", "Q4 2026" or "2026".
func (r Recurring) Period() string { return PeriodOf(r.NextDue, r.Interval) }

// PeriodOf names the period starting at date for an interval.
func PeriodOf(date, interval string) string {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return ""
	}
	switch interval {
	case "monthly":
		return d.Format("01/2006")
	case "quarterly":
		return fmt.Sprintf("Q%d %d", (int(d.Month())-1)/3+1, d.Year())
	}
	return d.Format("2006")
}

// NextDate is date moved on by one interval. A month end stays a month end
// and a day past the end of a shorter month is clamped to it, so 31 January
// is followed by 28 February and then 31 March, never by 3 March.
func NextDate(date, interval string) (string, error) {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", fmt.Errorf("the next date must be YYYY-MM-DD")
	}
	months := map[string]int{"monthly": 1, "quarterly": 3, "yearly": 12}[interval]
	if months == 0 {
		return "", fmt.Errorf("unknown interval %q", interval)
	}
	lastOf := func(y int, m time.Month) int { return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day() }
	endOfMonth := d.Day() == lastOf(d.Year(), d.Month())
	first := time.Date(d.Year(), d.Month()+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	day := min(d.Day(), lastOf(first.Year(), first.Month()))
	if endOfMonth {
		day = lastOf(first.Year(), first.Month())
	}
	return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, time.UTC).Format("2006-01-02"), nil
}

func (r Recurring) validate() error {
	switch {
	case r.ContactID <= 0:
		return fmt.Errorf("a recurring invoice needs a customer")
	case strings.TrimSpace(r.Name) == "":
		return fmt.Errorf("a recurring invoice needs a name")
	case len(r.Lines) == 0:
		return fmt.Errorf("a recurring invoice needs at least one position")
	}
	if _, err := NextDate(r.NextDue, r.Interval); err != nil {
		return err
	}
	return nil
}

// SaveRecurring creates a recurring invoice when ID is 0, else updates it.
func (s *Store) SaveRecurring(ctx context.Context, r Recurring) (int64, error) {
	if err := r.validate(); err != nil {
		return 0, err
	}
	lines, err := json.Marshal(r.Lines)
	if err != nil {
		return 0, err
	}
	active := 0
	if r.Active {
		active = 1
	}
	if r.ID == 0 {
		var id int64
		err := s.DB.QueryRowContext(ctx, s.Rebind(`INSERT INTO recurring_invoices
			(contact_id, name, frequency, next_due, lines, note, active, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`),
			r.ContactID, strings.TrimSpace(r.Name), r.Interval, r.NextDue, string(lines), r.Note, active, time.Now().UTC()).Scan(&id)
		return id, err
	}
	res, err := s.DB.ExecContext(ctx, s.Rebind(`UPDATE recurring_invoices SET contact_id = ?, name = ?, frequency = ?,
		next_due = ?, lines = ?, note = ?, active = ? WHERE id = ?`),
		r.ContactID, strings.TrimSpace(r.Name), r.Interval, r.NextDue, string(lines), r.Note, active, r.ID)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrNotFound
	}
	return r.ID, nil
}

const recurringSelect = `SELECT r.id, r.contact_id, c.name, r.name, r.frequency, r.next_due, r.lines, r.note, r.active, r.created_at,
	COALESCE((SELECT v.number FROM invoices v WHERE v.recurring_id = r.id ORDER BY v.issue_date DESC, v.id DESC LIMIT 1), ''),
	COALESCE((SELECT v.issue_date FROM invoices v WHERE v.recurring_id = r.id ORDER BY v.issue_date DESC, v.id DESC LIMIT 1), '')
	FROM recurring_invoices r JOIN contacts c ON c.id = r.contact_id`

func scanRecurring(row interface{ Scan(...any) error }) (Recurring, error) {
	var (
		r      Recurring
		lines  string
		active int
	)
	if err := row.Scan(&r.ID, &r.ContactID, &r.ContactName, &r.Name, &r.Interval, &r.NextDue, &lines, &r.Note,
		&active, &r.CreatedAt, &r.LastNumber, &r.LastDate); err != nil {
		return Recurring{}, err
	}
	r.Active = active == 1
	return r, json.Unmarshal([]byte(lines), &r.Lines)
}

// GetRecurring returns one recurring invoice.
func (s *Store) GetRecurring(ctx context.Context, id int64) (Recurring, error) {
	r, err := scanRecurring(s.DB.QueryRowContext(ctx, s.Rebind(recurringSelect+` WHERE r.id = ?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return Recurring{}, ErrNotFound
	}
	return r, err
}

// ListRecurring lists recurring invoices, the next due first; contactID 0
// lists all.
func (s *Store) ListRecurring(ctx context.Context, contactID int64) ([]Recurring, error) {
	q, args := recurringSelect, []any{}
	if contactID != 0 {
		q, args = q+` WHERE r.contact_id = ?`, append(args, contactID)
	}
	rows, err := s.DB.QueryContext(ctx, s.Rebind(q+` ORDER BY r.active DESC, r.next_due, r.id`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recurring
	for rows.Next() {
		r, err := scanRecurring(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DueRecurring lists the active recurring invoices due within days.
func (s *Store) DueRecurring(ctx context.Context, today time.Time, days int) ([]Recurring, error) {
	all, err := s.ListRecurring(ctx, 0)
	if err != nil {
		return nil, err
	}
	var out []Recurring
	for _, r := range all {
		if r.Due(today, days) {
			out = append(out, r)
		}
	}
	return out, nil
}

// SkipRecurring moves the next date on by one interval without an invoice,
// for a period that is not billed.
func (s *Store) SkipRecurring(ctx context.Context, id int64) error {
	r, err := s.GetRecurring(ctx, id)
	if err != nil {
		return err
	}
	next, err := NextDate(r.NextDue, r.Interval)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, s.Rebind(`UPDATE recurring_invoices SET next_due = ? WHERE id = ?`), next, id)
	return err
}

// SetRecurringActive pauses or resumes a recurring invoice.
func (s *Store) SetRecurringActive(ctx context.Context, id int64, active bool) error {
	v := 0
	if active {
		v = 1
	}
	_, err := s.DB.ExecContext(ctx, s.Rebind(`UPDATE recurring_invoices SET active = ? WHERE id = ?`), v, id)
	return err
}

// ErrRecurringUsed is returned when a recurring invoice with issued invoices
// is deleted: the invoices keep pointing at it, so it is paused instead.
var ErrRecurringUsed = errors.New("invoices were issued from this recurring invoice; pause it instead")

// DeleteRecurring removes a recurring invoice nothing was issued from.
func (s *Store) DeleteRecurring(ctx context.Context, id int64) error {
	var n int
	if err := s.DB.QueryRowContext(ctx, s.Rebind(`SELECT COUNT(*) FROM invoices WHERE recurring_id = ?`), id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrRecurringUsed
	}
	_, err := s.DB.ExecContext(ctx, s.Rebind(`DELETE FROM recurring_invoices WHERE id = ?`), id)
	return err
}

// advanceRecurring moves a recurring invoice's next date on by one interval,
// inside the transaction that issues its invoice.
func (s *Store) advanceRecurring(ctx context.Context, tx *sql.Tx, id int64) error {
	var due, freq string
	err := tx.QueryRowContext(ctx, s.Rebind(`SELECT next_due, frequency FROM recurring_invoices WHERE id = ?`), id).Scan(&due, &freq)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("the recurring invoice does not exist")
	}
	if err != nil {
		return err
	}
	next, err := NextDate(due, freq)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, s.Rebind(`UPDATE recurring_invoices SET next_due = ? WHERE id = ?`), next, id)
	return err
}

// nullID stores 0 as NULL, for optional references.
func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
