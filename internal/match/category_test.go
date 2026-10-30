package match

import "testing"

func TestCategoryFor(t *testing.T) {
	cats := []Category{
		{ID: 1, Name: "Miete", Direction: "outgoing"},
		{ID: 2, Name: "Telefon & Internet", Direction: "outgoing", Keywords: []string{"magenta", "a1", "drei"}},
		{ID: 3, Name: "Lebensmittel & Bewirtung", Direction: "outgoing", Keywords: []string{"billa", "spar", "hofer"}},
		{ID: 4, Name: "Bürobedarf", Direction: "outgoing", Keywords: []string{"libro", "pagro"}},
		{ID: 5, Name: "Erlöse Beratung", Direction: "income"},
		{ID: 6, Name: "Software", Direction: "both", Keywords: []string{"github", "jetbrains"}},
	}
	for _, c := range []struct {
		text, dir string
		want      int64 // 0 = no hit
	}{
		{"Kartenzahlung BILLA DANKT 1010 WIEN", "outgoing", 3},
		{"Empfänger: Hausverwaltung Huber Verwendungszweck: Miete September", "outgoing", 1},
		{"MAGENTA TELEKOM Rechnung 09/2026", "outgoing", 2},
		{"A1 Telekom Austria Rechnung", "outgoing", 2},             // whole word
		{"Kundennr A12345 Lieferung", "outgoing", 0},               // "a1" inside a number is not "A1"
		{"Telefonrechnung September", "outgoing", 2},               // name word at a word start
		{"BUEROBEDARF ONLINE", "outgoing", 4},                      // umlaut folding
		{"GITHUB, INC. Copilot", "income", 6},                      // "both" fits income too
		{"Beratung Juli", "income", 5},                             // name on the income side
		{"Beratung Juli", "outgoing", 0},                           // but not for an expense
		{"SPAR Lebensmittel und MAGENTA Gutschein", "outgoing", 0}, // two keywords of equal length: a tie, no guess
		{"HOFER KG Filiale", "outgoing", 3},
		{"Ueberweisung ohne Bezug", "outgoing", 0},
	} {
		hit, ok := CategoryFor(c.text, c.dir, cats)
		if (c.want == 0) == ok || (ok && hit.ID != c.want) {
			t.Errorf("CategoryFor(%q, %s) = %+v %v, want %d", c.text, c.dir, hit, ok, c.want)
		}
	}
	if hit, _ := CategoryFor("BILLA", "outgoing", cats); hit.Reason != "Stichwort „billa“" {
		t.Errorf("reason = %q", hit.Reason)
	}
}
