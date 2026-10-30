package main

import (
	"os"
	"strings"
	"testing"
)

func TestSection(t *testing.T) {
	log := "# Changelog\n\n## [Unreleased]\n\n## [1.2.0] - 2026-10-16\n\n### Added\n\n- A\n\n## [1.1.0] - 2026-01-01\n\n- B\n\n[1.2.0]: https://x\n"
	got, err := Section(log, "1.2.0")
	if err != nil || got != "### Added\n\n- A" {
		t.Errorf("1.2.0 = %q, %v", got, err)
	}
	if got, _ := Section(log, "1.1.0"); got != "- B" {
		t.Errorf("1.1.0 = %q", got)
	}
	if _, err := Section(log, "9.9.9"); err == nil {
		t.Error("a missing version was accepted")
	}
	if _, err := Section(log, "Unreleased"); err == nil {
		t.Error("an empty section was accepted")
	}
}

// The real changelog has a section for the newest tag it links to.
func TestRepositoryChangelog(t *testing.T) {
	data, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if notes, err := Section(string(data), "0.1.0"); err != nil || !strings.Contains(notes, "Invoices") {
		t.Errorf("0.1.0 section: %v", err)
	}
}
