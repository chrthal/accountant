package store

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// A migration that writes rows must be in dataMigrations, or the browser
// restore would load an older backup without it (ADR 0030).
func TestDataMigrationsAreListed(t *testing.T) {
	writes := regexp.MustCompile(`(?im)^\s*(INSERT|UPDATE|DELETE)\b`)
	for _, dialect := range []Dialect{SQLite, Postgres} {
		s := &Store{Dialect: dialect}
		names, err := s.embeddedMigrations()
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			// The first migration only records where the schema began.
			if name == "0001_init.sql" {
				continue
			}
			body, err := migrationFS.ReadFile("migrations/" + string(dialect) + "/" + name)
			if err != nil {
				t.Fatal(err)
			}
			if stmt := writes.FindString(string(body)); stmt != "" && !slices.Contains(dataMigrations, name) {
				t.Errorf("%s/%s writes rows (%s) but is not in dataMigrations", dialect, name, strings.TrimSpace(stmt))
			}
		}
	}
}
