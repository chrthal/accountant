package report

import "testing"

func TestDepreciation(t *testing.T) {
	cases := []struct {
		name  string
		cost  int64
		years int
		from  string
		want  map[int]int64
	}{
		{"first half: full year", 300000, 3, "2026-03-15", map[int]int64{2026: 100000, 2027: 100000, 2028: 100000}},
		{"second half: half year, extra year at the end", 300000, 3, "2026-07-01", map[int]int64{2026: 50000, 2027: 100000, 2028: 100000, 2029: 50000}},
		{"30 June is still the first half", 120000, 2, "2026-06-30", map[int]int64{2026: 60000, 2027: 60000}},
		{"odd cents go to the last year", 100001, 3, "2026-01-10", map[int]int64{2026: 33333, 2027: 33333, 2028: 33335}},
		{"one year, second half", 150000, 1, "2026-11-02", map[int]int64{2026: 75000, 2027: 75000}},
		{"bad date", 1000, 3, "", map[int]int64{}},
	}
	for _, c := range cases {
		got := Depreciation(c.cost, c.years, c.from)
		var sum int64
		for _, a := range got {
			sum += a
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
			continue
		}
		for y, a := range c.want {
			if got[y] != a {
				t.Errorf("%s: %d = %d, want %d (%v)", c.name, y, got[y], a, got)
			}
		}
		if len(c.want) > 0 && sum != c.cost {
			t.Errorf("%s: adds up to %d, cost %d", c.name, sum, c.cost)
		}
	}
	if v := BookValue(300000, 3, "2026-07-01", 2027); v != 150000 {
		t.Errorf("book value end of 2027 = %d, want 150000", v)
	}
	if v := BookValue(300000, 3, "2026-07-01", 2025); v != 300000 {
		t.Errorf("book value before use = %d", v)
	}
	if v := BookValue(300000, 3, "2026-07-01", 2030); v != 0 {
		t.Errorf("book value after the last year = %d", v)
	}
}
