package store

import "context"

// PastBooking is an earlier booking as a template for a new one (ADR 0044):
// the names it can be recognised by, newest first.
type PastBooking struct {
	EntryID   int64
	Direction string
	BookedOn  string
	Names     []string // contact, bank counterparty, description
}

// pastBookingsLimit bounds the history compared against. A year of bookings
// for a small business fits easily; older ones rarely add a new party.
const pastBookingsLimit = 1500

// PastBookings lists effective bookings that have a category, newest first,
// for "similar to an earlier booking". Invoice payments are left out, as in
// PreviousBooking: they are no template for anything.
func (s *Store) PastBookings(ctx context.Context) ([]PastBooking, error) {
	rows, err := s.DB.QueryContext(ctx, s.Rebind(`
		SELECT e.id, e.direction, e.booked_on, COALESCE(c.name, ''),
		       COALESCE((SELECT b.counterparty FROM bank_matches m JOIN bank_transactions b ON b.id = m.bank_transaction_id
		                 WHERE m.entry_id = e.id ORDER BY b.id LIMIT 1), ''),
		       `+currentDescription+`
		FROM entries e
		LEFT JOIN contacts c ON c.id = e.contact_id
		WHERE e.category_id IS NOT NULL AND e.invoice_id IS NULL
		  AND e.reverses_entry_id IS NULL
		  AND NOT EXISTS (SELECT 1 FROM entries r WHERE r.reverses_entry_id = e.id)
		ORDER BY e.booked_on DESC, e.id DESC
		LIMIT ?`), pastBookingsLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PastBooking
	for rows.Next() {
		var (
			p                      PastBooking
			contact, counter, desc string
		)
		if err := rows.Scan(&p.EntryID, &p.Direction, &p.BookedOn, &contact, &counter, &desc); err != nil {
			return nil, err
		}
		for _, n := range []string{contact, counter, desc} {
			if n != "" {
				p.Names = append(p.Names, n)
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
