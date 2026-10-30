package invoice

import (
	"fmt"
	"strings"
)

// EPCPayload returns the text of a SEPA payment QR code ("EPC QR code",
// EPC069-12 version 002), which Austrian banking apps read as the Stuzza
// QR code. Version 002 makes the BIC optional inside the EEA. The invoice
// number goes into the unstructured remittance text, so the payment comes
// back with it and bank matching finds the invoice (ADR 0020, ADR 0025).
func EPCPayload(name, iban, bic string, cents int64, remittance string) (string, error) {
	iban = strings.ToUpper(strings.ReplaceAll(iban, " ", ""))
	if len(iban) < 15 || len(iban) > 34 {
		return "", fmt.Errorf("IBAN %q has the wrong length", iban)
	}
	if cents < 1 || cents > 99_999_999_999 {
		return "", fmt.Errorf("amount out of range for a payment code")
	}
	name = truncateRunes(strings.TrimSpace(name), 70)
	if name == "" {
		return "", fmt.Errorf("a payment code needs the payee name")
	}
	lines := []string{
		"BCD", "002", "1", "SCT",
		strings.ToUpper(strings.TrimSpace(bic)),
		name,
		iban,
		fmt.Sprintf("EUR%d.%02d", cents/100, cents%100),
		"", // purpose code
		"", // structured creditor reference
		truncateRunes(remittance, 140),
	}
	return strings.Join(lines, "\n"), nil
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// groupIBAN writes an IBAN in blocks of four, as it is printed on paper.
func groupIBAN(iban string) string {
	iban = strings.ToUpper(strings.ReplaceAll(iban, " ", ""))
	var b strings.Builder
	for i, r := range iban {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}
