package bankcsv

import (
	"regexp"
	"sort"
	"strings"
)

// Some exports have no counterparty column. meinELBA (Raiffeisen) puts
// everything into one text field instead: "Empfänger: Hosting AG IBAN
// Empfänger: AT00… Verwendungszweck: Server August" (plan 0001, Phase 3).
// SplitText reads such labelled text back into its parts.
//
// It only acts on labels it knows. Text without them, such as "Kartenzahlung
// Billa 1010 Wien", comes back unsplit rather than guessed at.

// Labelled is a bank text split into its labelled parts.
type Labelled struct {
	Counterparty string
	IBAN         string
	Purpose      string // Verwendungszweck and Zahlungsreferenz, joined
}

type textLabel struct {
	label string
	field string
}

// The longer labels are matched first, so "IBAN Empfänger:" is not read as
// "Empfänger:" followed by an IBAN.
var textLabels = []textLabel{
	{"IBAN Empfänger:", "iban"}, {"IBAN Auftraggeber:", "iban"},
	{"BIC Empfänger:", "bic"}, {"BIC Auftraggeber:", "bic"},
	{"Empfänger:", "counterparty"}, {"Auftraggeber:", "counterparty"},
	{"Verwendungszweck:", "purpose"}, {"Zahlungsreferenz:", "reference"},
	{"Mandatsreferenz:", "other"}, {"Mandat:", "other"},
	{"Creditor ID:", "other"}, {"Gläubiger-ID:", "other"},
}

var labelRe = func() *regexp.Regexp {
	sorted := append([]textLabel(nil), textLabels...)
	sort.SliceStable(sorted, func(i, j int) bool { return len(sorted[i].label) > len(sorted[j].label) })
	parts := make([]string, len(sorted))
	for i, l := range sorted {
		parts[i] = regexp.QuoteMeta(l.label)
	}
	return regexp.MustCompile(`(?i)(` + strings.Join(parts, "|") + `)`)
}()

// SplitText splits labelled bank text. ok is false when the text carries no
// counterparty label, and the caller then keeps the text as it was.
func SplitText(s string) (Labelled, bool) {
	locs := labelRe.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return Labelled{}, false
	}
	fields := map[string]string{}
	for i, loc := range locs {
		end := len(s)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		value := strings.Join(strings.Fields(s[loc[1]:end]), " ")
		label := s[loc[0]:loc[1]]
		for _, l := range textLabels {
			if strings.EqualFold(l.label, label) {
				if fields[l.field] == "" {
					fields[l.field] = value
				}
				break
			}
		}
	}
	if fields["counterparty"] == "" {
		return Labelled{}, false
	}
	purpose := fields["purpose"]
	if ref := fields["reference"]; ref != "" {
		purpose = strings.TrimSpace(purpose + " " + ref)
	}
	return Labelled{
		Counterparty: fields["counterparty"],
		IBAN:         strings.ToUpper(strings.ReplaceAll(fields["iban"], " ", "")),
		Purpose:      purpose,
	}, true
}
