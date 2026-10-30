package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/store"
)

var originalPDF = []byte("%PDF-1.4\n% an original invoice\n")

// ADR 0031: an imported number that fits the scheme takes its place in the
// sequence, others go to the import range, and new numbers never reuse one.
func TestImportInvoice(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		contactID := setupIssuer(t, st, false)
		original, err := store.NewAttachment("E260112001_Beispiel.pdf", originalPDF)
		if err != nil {
			t.Fatal(err)
		}
		imp := func(number, date string) (store.Invoice, error) {
			return st.ImportInvoice(ctx, store.ImportedInvoice{
				Number: number, Original: original,
				NewInvoice: store.NewInvoice{ContactID: contactID, IssueDate: date, ServiceDate: date,
					Lines: []store.InvoiceLine{{Description: "Website", QuantityMilli: 1000, UnitPriceCents: 170000, VATRate: 20}}},
			})
		}
		issue := func(date string) string {
			t.Helper()
			inv, err := st.CreateInvoice(ctx, store.NewInvoice{ContactID: contactID, IssueDate: date, ServiceDate: date,
				Lines: []store.InvoiceLine{{Description: "Beratung", QuantityMilli: 1000, UnitPriceCents: 10000, VATRate: 20}}})
			if err != nil {
				t.Fatalf("issue %s: %v", date, err)
			}
			return inv.Number
		}
		scheme := func(numbering, prefix string) {
			s, _ := st.Settings(ctx)
			s.InvoiceNumbering, s.InvoicePrefix = numbering, prefix
			if err := st.SaveSettings(ctx, s); err != nil {
				t.Fatal(err)
			}
		}

		// Day scheme: the imported number continues the day's count.
		scheme(store.NumberingDay, "E")
		inv, err := imp("E260112001", "2026-01-12")
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		if !inv.Imported || inv.GrossCents != 204000 || inv.Number != "E260112001" {
			t.Errorf("imported = %+v", inv)
		}
		if got := issue("2026-01-12"); got != "E260112002" {
			t.Errorf("next number after the import = %s, want E260112002", got)
		}
		if _, err := imp("E260112001", "2026-01-12"); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Errorf("a duplicate number = %v", err)
		}

		// A number from another scheme goes to the import range.
		if _, err := imp("RE/2025/17", "2026-02-01"); err != nil {
			t.Fatalf("foreign number: %v", err)
		}
		// Year scheme without prefix: "2026-0003" was imported while the
		// prefix was "E", so it fits no scheme then; issued numbers later
		// skip it rather than collide.
		if _, err := imp("2026-0003", "2026-03-01"); err != nil {
			t.Fatal(err)
		}
		scheme(store.NumberingYear, "")
		got := []string{issue("2026-04-01"), issue("2026-04-02"), issue("2026-04-03")}
		if strings.Join(got, " ") != "2026-0001 2026-0002 2026-0004" {
			t.Errorf("year numbers = %v, want 2026-0001 2026-0002 2026-0004", got)
		}

		o, err := st.InvoiceOriginal(ctx, inv.ID)
		if err != nil || string(o.Data) != string(originalPDF) || o.Filename != "E260112001_Beispiel.pdf" {
			t.Errorf("original = %+v, %v", o, err)
		}
		if _, err := st.DB.ExecContext(ctx, `DELETE FROM invoice_originals`); err == nil {
			t.Error("an original could be deleted")
		}

		notPDF, _ := store.NewAttachment("x.png", []byte("\x89PNG\r\n\x1a\n0000000000000"))
		if _, err := st.ImportInvoice(ctx, store.ImportedInvoice{Number: "X1", Original: notPDF,
			NewInvoice: store.NewInvoice{ContactID: contactID, IssueDate: "2026-05-01", ServiceDate: "2026-05-01"}}); err == nil {
			t.Error("a non-PDF original was accepted")
		}
	})
}
