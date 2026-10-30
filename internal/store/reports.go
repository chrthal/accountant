package store

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/chrthal/accountant/internal/export"
	"github.com/chrthal/accountant/internal/report"
)

// YearRevenues returns the effective income of a year in booking order, which
// is what the Kleinunternehmer threshold is measured against (ADR 0010).
//
// Effective means neither a Storno nor reversed: a correction must not inflate
// the figure the exemption depends on.
func (s *Store) YearRevenues(ctx context.Context, year int) ([]report.Revenue, error) {
	const q = `
		SELECT e.id, e.booked_on, `+currentDescription+`, e.amount_gross_cents
		FROM entries e
		WHERE e.direction = 'income'
		  AND e.booked_on >= ? AND e.booked_on <= ?
		  AND e.reverses_entry_id IS NULL
		  AND NOT EXISTS (SELECT 1 FROM entries r WHERE r.reverses_entry_id = e.id)
		ORDER BY e.booked_on, e.id`

	rows, err := s.DB.QueryContext(ctx, s.Rebind(q),
		fmt.Sprintf("%04d-01-01", year), fmt.Sprintf("%04d-12-31", year))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []report.Revenue
	for rows.Next() {
		var r report.Revenue
		if err := rows.Scan(&r.EntryID, &r.BookedOn, &r.Description, &r.GrossCents); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Threshold computes the Kleinunternehmer tracker for a year.
func (s *Store) Threshold(ctx context.Context, year int) (report.Threshold, error) {
	revenues, err := s.YearRevenues(ctx, year)
	if err != nil {
		return report.Threshold{}, err
	}
	return report.ComputeThreshold(year, revenues), nil
}

// BookingsForExport returns a year's entries in the exporters' shape, oldest
// first — the order a Buchungsstapel is read in.
func (s *Store) BookingsForExport(ctx context.Context, year int) ([]export.Booking, error) {
	rows, err := s.DB.QueryContext(ctx,
		s.Rebind(entrySelect+` WHERE e.booked_on >= ? AND e.booked_on <= ?
			ORDER BY e.booked_on, e.id`),
		fmt.Sprintf("%04d-01-01", year), fmt.Sprintf("%04d-12-31", year))
	if err != nil {
		return nil, err
	}
	entries, err := scanEntries(rows)
	if err != nil {
		return nil, err
	}

	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	cats, err := s.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	share := map[int64]int{}
	for _, c := range cats {
		share[c.ID] = c.Deductible()
	}
	assets, err := s.Assets(ctx)
	if err != nil {
		return nil, err
	}
	isAsset := map[int64]bool{}
	for _, a := range assets {
		isAsset[a.EntryID] = true
	}
	out := make([]export.Booking, 0, len(entries))
	for _, e := range entries {
		var notes []string
		if e.IsMixed() {
			n := fmt.Sprintf("betrieblich %d %%, privat %d %%", e.BusinessPct, e.PrivatePct())
			if e.BusinessNote != "" {
				n += " (" + e.BusinessNote + ")"
			}
			notes = append(notes, n)
		}
		switch {
		case isAsset[e.ID]:
			notes = append(notes, "Anlage, AfA siehe Anlagenverzeichnis")
		case e.Direction == "outgoing" && e.CategoryID != nil && share[*e.CategoryID] < 100:
			notes = append(notes, fmt.Sprintf("%d %% absetzbar", share[*e.CategoryID]))
		}
		note := strings.Join(notes, "; ")
		out = append(out, export.Booking{
			Note: note,
			ID:   e.ID, BookedOn: e.BookedOn, Direction: e.Direction,
			Description: e.Description, AmountGrossCents: e.AmountGrossCents,
			VATRate: e.VATRate, VATCents: e.VATCents, NetCents: e.NetCents, BusinessPct: e.BusinessPct,
			Konto: e.Konto, Gegenkonto: e.Gegenkonto, ContactName: e.ContactName,
			IsReversal: e.IsReversal(), ReversesEntryID: e.ReversesEntryID,
			IsReversed: e.IsReversed(),
			// VAT on an expense in the exempt period is not Vorsteuer.
			VATNotDeductible: e.Direction == "outgoing" && settings.KUOn(e.BookedOn),
		})
	}
	return out, nil
}

// DATEVConfig reads the DATEV identifiers from settings. They start empty and
// the export refuses to run until the Steuerberater has supplied them.
func (s *Store) DATEVConfig(ctx context.Context, year int) (export.DATEVConfig, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT "key", value FROM settings`)
	if err != nil {
		return export.DATEVConfig{}, err
	}
	defer rows.Close()

	cfg := export.DATEVConfig{
		AccountLength:   4,
		FiscalYearStart: fmt.Sprintf("%04d-01-01", year),
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return export.DATEVConfig{}, err
		}
		switch k {
		case "datev_consultant_number":
			cfg.ConsultantNumber = v
		case "datev_client_number":
			cfg.ClientNumber = v
		case "datev_account_length":
			if n := atoiOr(v, 0); n > 0 {
				cfg.AccountLength = n
			}
		}
	}
	return cfg, rows.Err()
}

func atoiOr(s string, fallback int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return fallback
	}
	return n
}

// CategoryTotals is one row of the per-category breakdown. ID 0 collects the
// bookings without a category.
type CategoryTotals struct {
	ID                 int64
	Name               string
	Count              int
	IncomeGrossCents   int64
	OutgoingGrossCents int64
	IncomeNetCents     int64
	OutgoingNetCents   int64
	// OutgoingCostCents is net where the VAT is deductible and gross where it
	// is not, the cost the business actually carries (ADR 0022).
	OutgoingCostCents int64
}

// AfARow is the ID of the breakdown's depreciation row.
const AfARow int64 = -1

// SaldoCents is income net of VAT minus the cost of expenses.
func (c CategoryTotals) SaldoCents() int64 { return c.IncomeNetCents - c.OutgoingCostCents }

// CategoryBreakdown sums a year's effective bookings per category, biggest
// movement first. Net figures are included because, outside the
// Kleinunternehmer case, net is what the income statement is built on.
func (s *Store) CategoryBreakdown(ctx context.Context, year int) ([]CategoryTotals, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	ku, kuArgs := settings.kuSQL("e.booked_on")
	q := `
		SELECT COALESCE(g.id, 0), COALESCE(g.name, ''), COUNT(*),
		  COALESCE(SUM(CASE WHEN e.direction = 'income'   THEN e.amount_gross_cents ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN e.direction = 'outgoing' THEN e.amount_gross_cents ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN e.direction = 'income'   THEN e.net_cents ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN e.direction = 'outgoing' THEN e.net_cents ELSE 0 END), 0),
		  COALESCE(SUM(` + costSQL(ku, false) + `), 0)
		FROM entries e
		LEFT JOIN categories g ON g.id = e.category_id
		WHERE e.booked_on >= ? AND e.booked_on <= ?
		  AND e.reverses_entry_id IS NULL
		  AND NOT EXISTS (SELECT 1 FROM entries r WHERE r.reverses_entry_id = e.id)
		GROUP BY COALESCE(g.id, 0), COALESCE(g.name, '')`

	rows, err := s.DB.QueryContext(ctx, s.Rebind(q), append(append([]any{}, kuArgs...),
		fmt.Sprintf("%04d-01-01", year), fmt.Sprintf("%04d-12-31", year))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CategoryTotals
	for rows.Next() {
		var c CategoryTotals
		if err := rows.Scan(&c.ID, &c.Name, &c.Count, &c.IncomeGrossCents, &c.OutgoingGrossCents,
			&c.IncomeNetCents, &c.OutgoingNetCents, &c.OutgoingCostCents); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	// Closed before the next query: SQLite has a single connection.
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// AfA is cost of the year without a booking of its own: one row, so the
	// Saldo column adds up to the profit (ADR 0037).
	afa, err := s.DepreciationFor(ctx, year)
	if err != nil {
		return nil, err
	}
	if afa > 0 {
		out = append(out, CategoryTotals{ID: AfARow, Name: "Abschreibung (AfA)", OutgoingCostCents: afa})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a := out[i].IncomeGrossCents + out[i].OutgoingGrossCents
		b := out[j].IncomeGrossCents + out[j].OutgoingGrossCents
		if a != b {
			return a > b
		}
		return out[i].Name < out[j].Name
	})
	return out, rows.Err()
}

// AssetRows is the year's Anlagenverzeichnis for the exports (ADR 0037).
func (s *Store) AssetRows(ctx context.Context, year int) ([]export.AssetRow, error) {
	assets, err := s.AssetsInYear(ctx, year)
	if err != nil {
		return nil, err
	}
	out := make([]export.AssetRow, 0, len(assets))
	for _, a := range assets {
		out = append(out, export.AssetRow{EntryID: a.EntryID, Name: a.Name, BookedOn: a.BookedOn, InUseFrom: a.InUseFrom,
			UsefulLifeYears: a.UsefulLifeYears, CostCents: a.CostCents, AfACents: a.AfA(year), BookValueCents: a.BookValue(year)})
	}
	return out, nil
}
