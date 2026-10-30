package store_test

import (
	"context"
	"testing"

	"github.com/chrthal/accountant/internal/match"
	"github.com/chrthal/accountant/internal/store"
)

// ADR 0027: a new installation gets the default categories, once; a database
// with anything in it gets none.
func TestSeedDefaultCategories(t *testing.T) {
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		added, err := st.SeedDefaultCategories(ctx)
		if err != nil || !added {
			t.Fatalf("seed on an empty database = %v, %v", added, err)
		}
		cats, err := st.ListCategories(ctx)
		if err != nil || len(cats) != len(store.DefaultCategories) {
			t.Fatalf("categories = %d, %v; want %d", len(cats), err, len(store.DefaultCategories))
		}
		if again, err := st.SeedDefaultCategories(ctx); err != nil || again {
			t.Errorf("second seed = %v, %v; want nothing added", again, err)
		}
	})
	openEach(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if _, err := st.CreateContact(ctx, store.Contact{Kind: "customer", Name: "Muster GmbH"}); err != nil {
			t.Fatal(err)
		}
		if added, err := st.SeedDefaultCategories(ctx); err != nil || added {
			t.Errorf("seed on a used database = %v, %v; want nothing added", added, err)
		}
	})
}

// Each keyword assigns a bank line to its own category and to no other: a
// keyword in two categories would make both of them miss (ADR 0021).
func TestDefaultCategoryKeywordsAreUnambiguous(t *testing.T) {
	var cats []match.Category
	for i, c := range store.DefaultCategories {
		cats = append(cats, match.Category{ID: int64(i + 1), Name: c.Name, Direction: c.Direction, Keywords: c.KeywordList()})
	}
	for i, c := range store.DefaultCategories {
		for _, k := range c.KeywordList() {
			hit, ok := match.CategoryFor("Zahlung "+k+" 2026", c.Direction, cats)
			if !ok || hit.ID != int64(i+1) {
				t.Errorf("keyword %q of %q: hit %v (%v)", k, c.Name, hit, ok)
			}
		}
	}
}

// Everyday bank texts that name no payee of a default category stay
// unassigned: a wrong guess costs more than no guess.
func TestDefaultCategoryKeywordsLeaveOrdinaryTextAlone(t *testing.T) {
	var cats []match.Category
	for i, c := range store.DefaultCategories {
		cats = append(cats, match.Category{ID: int64(i + 1), Name: c.Name, Direction: c.Direction, Keywords: c.KeywordList()})
	}
	for _, text := range []string{
		"Merkur Markt 1100 Wien", "Rate drei von zwölf", "Hotel Sacher WiFi", "AMAZON EU SARL",
		"PayPal Europe", "Billa Filiale 123", "Überweisung an Max Mustermann", "Hot Dog Stand",
		"Spar Supermarkt", "IKEA Vösendorf", "Jetzt kaufen Gutschein",
	} {
		if hit, ok := match.CategoryFor(text, "outgoing", cats); ok {
			t.Errorf("%q went to %q (%s)", text, store.DefaultCategories[hit.ID-1].Name, hit.Reason)
		}
	}
}
