package match

import "testing"

func TestSimilarName(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"MUSTER GMBH WIEN", "Muster GmbH", true},
		{"Müller & Söhne KG", "MUELLER SOEHNE", true},
		{"Alpenblick Hotel Ges.m.b.H.", "Alpenblick Hotel GmbH", true},
		{"Alpenblik Hotel", "Alpenblick Hotel GmbH", true}, // one typo
		{"Muster GmbH", "Mustermann GmbH", false},
		{"Hosting AG", "Muster GmbH", false},
		{"GmbH", "Muster GmbH", false}, // nothing left after legal forms
		{"", "Muster GmbH", false},
	} {
		if got := SimilarName(c.a, c.b); got != c.want {
			t.Errorf("SimilarName(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestNumbersInText(t *testing.T) {
	n := numbersIn("RE 2026-0001 vom 15.08.2026, auch 2026 0002 und 2026/0003")
	for _, want := range []string{"20260001", "15082026", "20260002", "20260003"} {
		if !n[want] {
			t.Errorf("%s not found in %v", want, n)
		}
	}
	if n["2026000115082026"] {
		t.Error("an invoice number and a date were merged into one number")
	}
}

func TestInvoiceHintsAreRankedAndExplained(t *testing.T) {
	open := []Invoice{
		{ID: 1, Number: "2026-0001", RecipientName: "Muster GmbH", OpenCents: 23500},
		{ID: 2, Number: "2026-0002", RecipientName: "Alpenblick Hotel GmbH", OpenCents: 23500},
		{ID: 3, Number: "2026-0003", RecipientName: "Muster GmbH", OpenCents: 99900},
	}

	// Number and amount: unambiguous, first, bookable.
	h := Invoices("Zahlungsreferenz: RE 2026-0001", "MUSTER GMBH", 23500, open)
	if len(h) != 2 || h[0].ID != 1 || !h[0].Sure() || !h[0].NameMatches {
		t.Fatalf("hints = %+v", h)
	}
	if h[1].ID != 2 || h[1].Sure() {
		t.Errorf("the amount-only invoice should follow, not be sure: %+v", h[1])
	}
	if r := h[0].Reasons(); len(r) != 3 {
		t.Errorf("reasons = %v", r)
	}

	// Same amount for two invoices, no number: the name decides the order,
	// and neither is sure.
	h = Invoices("Danke", "Alpenblick Hotel", 23500, open)
	if h[0].ID != 2 || h[0].Sure() {
		t.Errorf("name did not break the tie: %+v", h)
	}

	// The number with a short payment: a part payment, bookable one by one
	// but never sure enough for the bulk action (ADR 0036).
	h = Invoices("RE 2026-0003 Teilzahlung", "Muster GmbH", 50000, open)
	if len(h) != 1 || h[0].ID != 3 || !h[0].Partial || !h[0].Bookable() || h[0].Sure() {
		t.Errorf("short payment = %+v", h)
	}
	// More than is open is no part payment.
	if h := Invoices("RE 2026-0003", "Muster GmbH", 150000, open); len(h) != 1 || h[0].Bookable() {
		t.Errorf("overpayment = %+v", h)
	}

	if h := Invoices("Miete", "Hausverwaltung", 120000, open); len(h) != 0 {
		t.Errorf("unrelated transfer got hints: %+v", h)
	}
}

func TestSimilarParty(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"BILLA DANKT 1234 WIEN", "Billa Graz", true},
		{"POS 4711 K1 12.03. 14:20 BILLA DANKT 0815", "Billa", true},
		{"A1 TELEKOM AUSTRIA", "A1 Telekom Austria AG", true},
		{"Hetzner Online GmbH", "HETZNER ONLINE", true},
		{"Magenta Telekom", "A1 Telekom", false},
		{"Österreichische Post AG", "Österreichische Bundesbahnen", false},
		{"Rechnung 4711", "Rechnung 4712", false},
		{"", "Billa", false},
		{"Amazon EU S.a.r.L. Marketplace", "AMAZON MARKETPLACE", true},
	} {
		if got := SimilarParty(c.a, c.b); got != c.want {
			t.Errorf("SimilarParty(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
