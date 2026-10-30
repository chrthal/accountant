package store

import (
	"context"
	"fmt"
	"sort"
)

// RateSum is net and VAT at one rate.
type RateSum struct {
	Rate     int
	NetCents int64
	VATCents int64
}

// VATPeriod is one month or quarter of the VAT overview (plan 0002, Phase 11).
type VATPeriod struct {
	Label string
	From  string // 'YYYY-MM-DD'
	To    string

	Output         []RateSum // income, by rate: the VAT owed on turnover
	OutputNetCents int64
	OutputVATCents int64
	InputNetCents  int64 // outgoings: net of bookings with deductible VAT
	InputVATCents  int64 // deductible Vorsteuer
	ExemptNetCents int64 // income at 0 %
	// InputVATBlockedCents is VAT on expenses booked while the
	// Kleinunternehmerregelung applied: recorded, not deductible (ADR 0022).
	InputVATBlockedCents int64
	// InputVATPrivateCents is VAT on the private share of mixed expenses:
	// not Vorsteuer (ADR 0041).
	InputVATPrivateCents int64
	// KU says whether the exemption covered all, part or none of the period
	// (KUFull, KUPartial, KUNone).
	KU string
}

// PayableCents is output VAT minus input VAT: positive is a Zahllast,
// negative a Gutschrift.
func (p VATPeriod) PayableCents() int64 { return p.OutputVATCents - p.InputVATCents }

// VATPeriods sums a year's effective bookings per month (quarterly=false) or
// quarter, by the booking date. An expense used partly privately counts by
// its business share (ADR 0041).
//
// This is arithmetic over the ledger, not a USt-Voranmeldung. It assumes the
// booking date is the date that decides the period, which matches
// Istbesteuerung (taxation on receipt) for income. Whether that applies, and
// when Vorsteuer may be deducted (it needs the invoice, not only the
// payment), is for the Steuerberater. The page says so, and no Kennzahlen
// are assigned (plan 0002, section D).
func (s *Store) VATPeriods(ctx context.Context, year int, quarterly bool) ([]VATPeriod, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	ku, kuArgs := settings.kuSQL("e.booked_on")
	q := `
		SELECT SUBSTR(e.booked_on, 6, 2), e.direction, e.vat_rate,
		       CASE WHEN ` + ku + ` THEN 1 ELSE 0 END,
		       COALESCE(SUM(` + shareSQL("e.net_cents") + `), 0), COALESCE(SUM(` + businessVATSQL + `), 0),
		       COALESCE(SUM(e.vat_cents - ` + businessVATSQL + `), 0)
		FROM entries e
		WHERE e.booked_on >= ? AND e.booked_on <= ?
		  AND e.reverses_entry_id IS NULL
		  AND NOT EXISTS (SELECT 1 FROM entries r WHERE r.reverses_entry_id = e.id)
		GROUP BY SUBSTR(e.booked_on, 6, 2), e.direction, e.vat_rate, 4`

	n := 12
	if quarterly {
		n = 4
	}
	periods := make([]VATPeriod, n)
	monthNames := []string{"Jänner", "Februar", "März", "April", "Mai", "Juni", "Juli",
		"August", "September", "Oktober", "November", "Dezember"}
	lastDay := []int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
		lastDay[1] = 29
	}
	for i := range periods {
		first, last := i+1, i+1
		label := monthNames[i]
		if quarterly {
			first, last = i*3+1, i*3+3
			label = fmt.Sprintf("%d. Quartal", i+1)
		}
		periods[i] = VATPeriod{
			Label: label,
			From:  fmt.Sprintf("%04d-%02d-01", year, first),
			To:    fmt.Sprintf("%04d-%02d-%02d", year, last, lastDay[last-1]),
		}
		periods[i].KU = settings.KUCoverage(periods[i].From, periods[i].To)
	}

	rows, err := s.DB.QueryContext(ctx, s.Rebind(q), append(append([]any{}, kuArgs...),
		fmt.Sprintf("%04d-01-01", year), fmt.Sprintf("%04d-12-31", year))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byRate := make([]map[int]*RateSum, n)
	for rows.Next() {
		var (
			mm, dir  string
			rate     int
			inKU     int
			net, vat int64
			private  int64
			month    int
		)
		if err := rows.Scan(&mm, &dir, &rate, &inKU, &net, &vat, &private); err != nil {
			return nil, err
		}
		if _, err := fmt.Sscanf(mm, "%d", &month); err != nil || month < 1 || month > 12 {
			return nil, fmt.Errorf("unexpected month %q", mm)
		}
		i := month - 1
		if quarterly {
			i = (month - 1) / 3
		}
		p := &periods[i]
		switch {
		case dir == "income" && rate == 0:
			p.ExemptNetCents += net
		case dir == "income":
			if byRate[i] == nil {
				byRate[i] = map[int]*RateSum{}
			}
			rs := byRate[i][rate]
			if rs == nil {
				rs = &RateSum{Rate: rate}
				byRate[i][rate] = rs
			}
			rs.NetCents += net
			rs.VATCents += vat
			p.OutputNetCents += net
			p.OutputVATCents += vat
		case dir == "outgoing" && rate > 0 && inKU == 1:
			p.InputVATBlockedCents += vat + private
		case dir == "outgoing" && rate > 0:
			p.InputNetCents += net
			p.InputVATCents += vat
			p.InputVATPrivateCents += private
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range periods {
		for _, rs := range byRate[i] {
			periods[i].Output = append(periods[i].Output, *rs)
		}
		sort.Slice(periods[i].Output, func(a, b int) bool {
			return periods[i].Output[a].Rate > periods[i].Output[b].Rate
		})
	}
	return periods, nil
}
