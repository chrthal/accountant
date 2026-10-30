package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chrthal/accountant/internal/report"
)

// GWGLimitCents is the net cost up to which an asset may be expensed at
// once as a geringwertiges Wirtschaftsgut (§ 13 EStG, €1,000 since 2023).
const GWGLimitCents = 100_000

// Asset is an expense booking written off over its useful life (ADR 0037).
type Asset struct {
	ID              int64
	EntryID         int64
	Name            string
	CostCents       int64 // net, or gross when the VAT was not deductible
	InUseFrom       string
	UsefulLifeYears int
	CreatedAt       time.Time
	BookedOn        string // the booking's date, for display
}

// AfA is the asset's depreciation in a year; BookValue what is left at the
// end of it.
func (a Asset) AfA(year int) int64 {
	return report.Depreciation(a.CostCents, a.UsefulLifeYears, a.InUseFrom)[year]
}

func (a Asset) BookValue(year int) int64 {
	return report.BookValue(a.CostCents, a.UsefulLifeYears, a.InUseFrom, year)
}

// LastYear is the year the asset is written off completely.
func (a Asset) LastYear() int {
	last := 0
	for y := range report.Depreciation(a.CostCents, a.UsefulLifeYears, a.InUseFrom) {
		last = max(last, y)
	}
	return last
}

// CreateAsset writes off an expense booking over its useful life instead of
// counting it in the year it was paid. The cost is the booking's net, or its
// gross where the VAT was not deductible (the Kleinunternehmer period, ADR
// 0022); it is fixed now, like the booking's own split.
func (s *Store) CreateAsset(ctx context.Context, entryID int64, name string, years int, inUseFrom string) (int64, error) {
	e, err := s.GetEntry(ctx, entryID)
	if err != nil {
		return 0, err
	}
	switch {
	case e.Direction != "outgoing":
		return 0, fmt.Errorf("only an expense can be an asset")
	case e.ReversesEntryID != nil || e.ReversedByID != nil:
		return 0, fmt.Errorf("a reversed booking cannot be an asset")
	case years < 1 || years > 50:
		return 0, fmt.Errorf("the useful life must be between 1 and 50 years")
	}
	if _, err := time.Parse("2006-01-02", inUseFrom); err != nil {
		return 0, fmt.Errorf("the in-use date must be YYYY-MM-DD")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = e.Description
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return 0, err
	}
	cost := e.NetCents
	if settings.KUOn(e.BookedOn) {
		cost = e.AmountGrossCents
	}
	// Only the business share is written off; the rest is private (ADR 0041).
	cost = Share(cost, e.BusinessPct)
	var id int64
	err = s.DB.QueryRowContext(ctx, s.Rebind(`INSERT INTO assets (entry_id, name, cost_cents, in_use_from, useful_life_years, created_at)
		VALUES (?, ?, ?, ?, ?, ?) RETURNING id`), entryID, name, cost, inUseFrom, years, time.Now().UTC()).Scan(&id)
	if isUniqueViolation(err) {
		return 0, fmt.Errorf("this booking is an asset already")
	}
	return id, err
}

// DeleteAsset makes the booking an ordinary expense again.
func (s *Store) DeleteAsset(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, s.Rebind(`DELETE FROM assets WHERE id = ?`), id)
	return err
}

// effectiveAsset limits assets to bookings that are neither a Storno nor
// reversed: a reversed purchase was never made.
const effectiveAsset = `e.reverses_entry_id IS NULL AND NOT EXISTS (SELECT 1 FROM entries r WHERE r.reverses_entry_id = e.id)`

// Assets lists the assets of effective bookings, oldest first.
func (s *Store) Assets(ctx context.Context) ([]Asset, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT a.id, a.entry_id, a.name, a.cost_cents, a.in_use_from, a.useful_life_years,
		a.created_at, e.booked_on FROM assets a JOIN entries e ON e.id = a.entry_id WHERE `+effectiveAsset+`
		ORDER BY a.in_use_from, a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Asset
	for rows.Next() {
		var a Asset
		if err := rows.Scan(&a.ID, &a.EntryID, &a.Name, &a.CostCents, &a.InUseFrom, &a.UsefulLifeYears,
			&a.CreatedAt, &a.BookedOn); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AssetForEntry returns the asset a booking is, if it is one.
func (s *Store) AssetForEntry(ctx context.Context, entryID int64) (Asset, bool, error) {
	var a Asset
	err := s.DB.QueryRowContext(ctx, s.Rebind(`SELECT a.id, a.entry_id, a.name, a.cost_cents, a.in_use_from,
		a.useful_life_years, a.created_at, e.booked_on FROM assets a JOIN entries e ON e.id = a.entry_id WHERE a.entry_id = ?`), entryID).
		Scan(&a.ID, &a.EntryID, &a.Name, &a.CostCents, &a.InUseFrom, &a.UsefulLifeYears, &a.CreatedAt, &a.BookedOn)
	if errors.Is(err, sql.ErrNoRows) {
		return Asset{}, false, nil
	}
	return a, err == nil, err
}

// AssetsInYear lists the assets with depreciation or a book value in the
// year: the Anlagenverzeichnis of that year.
func (s *Store) AssetsInYear(ctx context.Context, year int) ([]Asset, error) {
	all, err := s.Assets(ctx)
	if err != nil {
		return nil, err
	}
	var out []Asset
	for _, a := range all {
		from, _ := time.Parse("2006-01-02", a.InUseFrom)
		booked, _ := time.Parse("2006-01-02", a.BookedOn)
		if (from.Year() <= year || booked.Year() <= year) && a.LastYear() >= year {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].InUseFrom < out[j].InUseFrom })
	return out, nil
}

// DepreciationFor is the year's AfA over all assets.
func (s *Store) DepreciationFor(ctx context.Context, year int) (int64, error) {
	all, err := s.Assets(ctx)
	if err != nil {
		return 0, err
	}
	var sum int64
	for _, a := range all {
		sum += a.AfA(year)
	}
	return sum, nil
}
