package store_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/chrthal/accountant/internal/store"
)

// ADR 0043: a description change is shown everywhere, the booking row stays
// as written, and the history keeps every version.
func TestRenameEntry(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		id := entry(t, st, "2026-03-01", "outgoing", "Rechnung 4711", 1200, 20, nil)
		if h, _ := st.DescriptionHistory(ctx, id); len(h) != 0 {
			t.Errorf("history of an unchanged booking = %+v", h)
		}
		if err := st.RenameEntry(ctx, id, "  "); err == nil {
			t.Error("an empty description was accepted")
		}
		if err := st.RenameEntry(ctx, id, "Rechnung 4711"); !errors.Is(err, store.ErrSameDescription) {
			t.Errorf("same description = %v", err)
		}
		for _, d := range []string{"Druckerpapier", "Druckerpapier A4"} {
			if err := st.RenameEntry(ctx, id, d); err != nil {
				t.Fatal(err)
			}
		}

		e, _ := st.GetEntry(ctx, id)
		if e.Description != "Druckerpapier A4" || e.AmountGrossCents != 1200 {
			t.Errorf("entry = %q %d", e.Description, e.AmountGrossCents)
		}
		h, err := st.DescriptionHistory(ctx, id)
		if err != nil || len(h) != 3 || !h[0].Original || h[0].Description != "Rechnung 4711" || h[2].Description != "Druckerpapier A4" {
			t.Errorf("history = %+v, %v", h, err)
		}
		if page, _ := st.FindEntries(ctx, store.EntryFilter{Query: "papier a4"}); len(page.Entries) != 1 {
			t.Error("search does not find the new description")
		}
		if page, _ := st.FindEntries(ctx, store.EntryFilter{Query: "4711"}); len(page.Entries) != 0 {
			t.Error("search still finds the old description")
		}
		if b, _ := st.BookingsForExport(ctx, 2026); len(b) != 1 || b[0].Description != "Druckerpapier A4" {
			t.Errorf("export = %+v", b)
		}

		// The changes are append-only too.
		if _, err := st.DB.ExecContext(ctx, `UPDATE entry_descriptions SET description = 'x'`); err == nil {
			t.Error("a description change was overwritten")
		}
		if _, err := st.DB.ExecContext(ctx, `DELETE FROM entry_descriptions`); err == nil {
			t.Error("a description change was deleted")
		}

		// A Storno names the booking as it is called now.
		storno, _ := st.ReverseEntry(ctx, id, "2026-03-05")
		if s, _ := st.GetEntry(ctx, storno); s.Description != "Storno zu #1: Druckerpapier A4" {
			t.Errorf("Storno = %q", s.Description)
		}

		var file bytes.Buffer
		if _, err := st.Backup(ctx, &file, "test"); err != nil {
			t.Fatal(err)
		}
		if sum, err := store.VerifyBackup(&file); err != nil || sum.Rows["entry_descriptions"] != 2 {
			t.Errorf("backup: %+v %v", sum.Rows, err)
		}
	})
}
