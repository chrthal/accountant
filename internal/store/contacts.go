package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Contact is a customer, a vendor, or both.
type Contact struct {
	ID        int64
	Kind      string // customer | vendor | both
	Name      string
	UID       string // UID/ATU number, needed on invoices where applicable (§ 11 UStG)
	Address   string
	Email     string
	CreatedAt time.Time
}

// ValidContactKinds are the accepted values for Contact.Kind.
var ValidContactKinds = []string{"customer", "vendor", "both"}

// Validate reports what is wrong with a contact, if anything.
func (c Contact) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("name is required")
	}
	for _, k := range ValidContactKinds {
		if c.Kind == k {
			return nil
		}
	}
	return fmt.Errorf("kind must be one of customer, vendor, both")
}

// CreateContact stores a new contact and returns its id.
func (s *Store) CreateContact(ctx context.Context, c Contact) (int64, error) {
	if err := c.Validate(); err != nil {
		return 0, err
	}
	q := s.Rebind(`INSERT INTO contacts (kind, name, uid, address, email, created_at)
	               VALUES (?, ?, ?, ?, ?, ?) RETURNING id`)
	var id int64
	err := s.DB.QueryRowContext(ctx, q,
		c.Kind, strings.TrimSpace(c.Name), c.UID, c.Address, c.Email, time.Now().UTC(),
	).Scan(&id)
	return id, err
}

// ListContacts returns all contacts, newest first.
func (s *Store) ListContacts(ctx context.Context) ([]Contact, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, kind, name, uid, address, email, created_at
		 FROM contacts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.ID, &c.Kind, &c.Name, &c.UID, &c.Address, &c.Email, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Category groups entries and can carry default Konto/Gegenkonto values. The
// defaults start empty on purpose — the chart of accounts is the user's
// Steuerberater's call, not something to seed with plausible guesses (ADR 0007).
type Category struct {
	ID                int64
	Name              string
	Direction         string // income | outgoing | both
	DefaultKonto      string
	DefaultGegenkonto string
	// Keywords assign bank lines to this category (ADR 0021), comma-separated.
	Keywords string
	// DefaultVATRate is the rate a booking here usually carries; nil = none.
	DefaultVATRate *int
	// DeductiblePct is the share of an expense here that counts as cost for
	// the profit, e.g. 50 for Bewirtung (ADR 0037). 0 means 100.
	DeductiblePct int
}

// Deductible is the category's deductible share in percent.
func (c Category) Deductible() int {
	if c.DeductiblePct == 0 {
		return 100
	}
	return c.DeductiblePct
}

// KeywordList returns the category's keywords, trimmed and lower-cased.
func (c Category) KeywordList() []string {
	var out []string
	for _, k := range strings.Split(c.Keywords, ",") {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// HasDefaultVAT reports whether the category sets a VAT rate, for templates.
func (c Category) HasDefaultVAT() bool { return c.DefaultVATRate != nil }

// VAT returns the default rate, or -1 when there is none, for templates.
func (c Category) VAT() int {
	if c.DefaultVATRate == nil {
		return -1
	}
	return *c.DefaultVATRate
}

// CreateCategory stores a new category and returns its id.
func (s *Store) CreateCategory(ctx context.Context, c Category) (int64, error) {
	if err := c.validate(); err != nil {
		return 0, err
	}
	q := s.Rebind(`INSERT INTO categories (name, direction, default_konto, default_gegenkonto,
	               keywords, default_vat_rate, deductible_pct) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`)
	var id int64
	err := s.DB.QueryRowContext(ctx, q,
		strings.TrimSpace(c.Name), c.Direction, c.DefaultKonto, c.DefaultGegenkonto,
		normaliseKeywords(c.Keywords), c.DefaultVATRate, c.Deductible()).Scan(&id)
	if isUniqueViolation(err) {
		return 0, fmt.Errorf("a category with this name already exists")
	}
	return id, err
}

// GetCategory returns one category by id.
func (s *Store) GetCategory(ctx context.Context, id int64) (Category, error) {
	var c Category
	err := s.DB.QueryRowContext(ctx, s.Rebind(
		`SELECT id, name, direction, default_konto, default_gegenkonto, keywords, default_vat_rate, deductible_pct
		 FROM categories WHERE id = ?`), id).
		Scan(&c.ID, &c.Name, &c.Direction, &c.DefaultKonto, &c.DefaultGegenkonto, &c.Keywords, nullInt{&c.DefaultVATRate}, &c.DeductiblePct)
	if err == sql.ErrNoRows {
		return Category{}, ErrNotFound
	}
	return c, err
}

// ListCategories returns all categories by name.
func (s *Store) ListCategories(ctx context.Context) ([]Category, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, direction, default_konto, default_gegenkonto, keywords, default_vat_rate, deductible_pct
		 FROM categories ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Direction, &c.DefaultKonto, &c.DefaultGegenkonto,
			&c.Keywords, nullInt{&c.DefaultVATRate}, &c.DeductiblePct); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetContact returns one contact.
func (s *Store) GetContact(ctx context.Context, id int64) (Contact, error) {
	c, err := s.contact(ctx, id)
	if err != nil && strings.Contains(err.Error(), "contact not found") {
		return Contact{}, ErrNotFound
	}
	return c, err
}

// UpdateContact changes a contact's details. Contacts are master data, not
// bookings: an invoice snapshots its recipient at issue time (ADR 0008), so
// editing a contact never rewrites an invoice that has gone out. Bookings show
// the contact's current name.
func (s *Store) UpdateContact(ctx context.Context, c Contact) error {
	if err := c.Validate(); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, s.Rebind(`UPDATE contacts
		SET kind = ?, name = ?, uid = ?, address = ?, email = ? WHERE id = ?`),
		c.Kind, strings.TrimSpace(c.Name), strings.TrimSpace(c.UID), c.Address, strings.TrimSpace(c.Email), c.ID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateCategory changes a category. Its account defaults are resolved onto a
// booking when the booking is made (ADR 0012), so this affects future bookings
// only. Existing ones keep the accounts they were booked with.
func (s *Store) UpdateCategory(ctx context.Context, c Category) error {
	if err := c.validate(); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, s.Rebind(`UPDATE categories
		SET name = ?, direction = ?, default_konto = ?, default_gegenkonto = ?,
		    keywords = ?, default_vat_rate = ?, deductible_pct = ? WHERE id = ?`),
		strings.TrimSpace(c.Name), c.Direction, strings.TrimSpace(c.DefaultKonto),
		strings.TrimSpace(c.DefaultGegenkonto), normaliseKeywords(c.Keywords), c.DefaultVATRate, c.Deductible(), c.ID)
	if isUniqueViolation(err) {
		return fmt.Errorf("a category with this name already exists")
	}
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

func (c Category) validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if c.DefaultVATRate != nil {
		switch *c.DefaultVATRate {
		case 0, 10, 13, 20:
		default:
			return fmt.Errorf("default VAT rate must be one of 0, 10, 13, 20")
		}
	}
	if c.DeductiblePct < 0 || c.DeductiblePct > 100 {
		return fmt.Errorf("the deductible share must be between 1 and 100 percent")
	}
	switch c.Direction {
	case "income", "outgoing", "both":
		return nil
	}
	return fmt.Errorf("direction must be one of income, outgoing, both")
}

// normaliseKeywords stores keywords as "a, b, c": trimmed, lower-cased,
// without empties or duplicates.
func normaliseKeywords(s string) string {
	seen := map[string]bool{}
	var out []string
	for _, k := range strings.Split(s, ",") {
		k = strings.ToLower(strings.Join(strings.Fields(k), " "))
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return strings.Join(out, ", ")
}

// nullInt scans a nullable integer column into an *int field.
type nullInt struct{ dst **int }

func (n nullInt) Scan(src any) error {
	var v sql.NullInt64
	if err := v.Scan(src); err != nil {
		return err
	}
	if !v.Valid {
		*n.dst = nil
		return nil
	}
	i := int(v.Int64)
	*n.dst = &i
	return nil
}

// ContactUsage is what refers to a contact.
type ContactUsage struct {
	Invoices  int
	Entries   int
	Recurring int // recurring invoices (ADR 0038)
}

// InUse reports whether anything refers to the contact.
func (u ContactUsage) InUse() bool { return u.Invoices > 0 || u.Entries > 0 || u.Recurring > 0 }

// ContactUsage counts the invoices and bookings that refer to a contact.
func (s *Store) ContactUsage(ctx context.Context, id int64) (ContactUsage, error) {
	var u ContactUsage
	err := s.DB.QueryRowContext(ctx, s.Rebind(`SELECT
		(SELECT COUNT(*) FROM invoices WHERE contact_id = ?),
		(SELECT COUNT(*) FROM entries  WHERE contact_id = ?),
		(SELECT COUNT(*) FROM recurring_invoices WHERE contact_id = ?)`), id, id, id).Scan(&u.Invoices, &u.Entries, &u.Recurring)
	return u, err
}

// ErrContactInUse is returned when a contact cannot be deleted.
var ErrContactInUse = errors.New("contact is in use")

// DeleteContact removes a contact that nothing refers to. A contact on an
// invoice or a booking stays: both are immutable records, and the reference
// cannot be cleared (ADR 0007, ADR 0008). Cached model suggestions that
// name the contact are derived data and go with it (ADR 0020).
func (s *Store) DeleteContact(ctx context.Context, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var inv, ent, rec int
	if err := tx.QueryRowContext(ctx, s.Rebind(`SELECT
		(SELECT COUNT(*) FROM invoices WHERE contact_id = ?),
		(SELECT COUNT(*) FROM entries  WHERE contact_id = ?),
		(SELECT COUNT(*) FROM recurring_invoices WHERE contact_id = ?)`), id, id, id).Scan(&inv, &ent, &rec); err != nil {
		return err
	}
	if inv > 0 || ent > 0 || rec > 0 {
		return fmt.Errorf("%w: %d invoice(s), %d booking(s), %d recurring invoice(s)", ErrContactInUse, inv, ent, rec)
	}
	if _, err := tx.ExecContext(ctx, s.Rebind(`UPDATE bank_suggestions SET contact_id = NULL WHERE contact_id = ?`), id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, s.Rebind(`DELETE FROM contacts WHERE id = ?`), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}
