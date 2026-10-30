package report

import "time"

// Depreciation is the linear AfA of one asset (§ 7 EStG, ADR 0037): the
// acquisition cost spread evenly over the useful life, starting in the year
// the asset is put in use. Used for six months or less in that first year
// (in use from 1 July on), it gets half a year's amount there (§ 7 Abs 2
// EStG), and the half left over falls into an extra year at the end. Cents
// that do not divide evenly go to the last year, so the amounts always add
// up to the cost exactly.
//
// It returns the AfA per calendar year. Degressive AfA and an asset leaving
// the business early are not modelled.
func Depreciation(costCents int64, years int, inUseFrom string) map[int]int64 {
	out := map[int]int64{}
	d, err := time.Parse("2006-01-02", inUseFrom)
	if err != nil || costCents <= 0 || years < 1 {
		return out
	}
	annual := costCents / int64(years)
	first := d.Year()
	periods := years
	if d.Month() >= time.July {
		periods = years + 1
	}
	var given int64
	for i := range periods {
		amount := annual
		if periods > years && i == 0 {
			amount = annual / 2
		}
		if i == periods-1 {
			amount = costCents - given
		}
		out[first+i] = amount
		given += amount
	}
	return out
}

// BookValue is what is left of the cost at the end of a year.
func BookValue(costCents int64, years int, inUseFrom string, year int) int64 {
	left := costCents
	for y, a := range Depreciation(costCents, years, inUseFrom) {
		if y <= year {
			left -= a
		}
	}
	d, err := time.Parse("2006-01-02", inUseFrom)
	if err == nil && year < d.Year() {
		return costCents
	}
	return left
}
