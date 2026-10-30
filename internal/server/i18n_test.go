package server

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/chrthal/accountant/internal/i18n"
)

// ADR 0024: every user-facing text goes through t/tf (templates) or
// s.T/s.Tf (handlers), and every such text has an English translation.

var (
	tmplMsg   = regexp.MustCompile(`\b(?:t|tf) "((?:[^"\\]|\\.)*)"`)
	goMsg     = regexp.MustCompile(`s\.Tf?\("((?:[^"\\]|\\.)*)"`)
	actionRe  = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	tagRe     = regexp.MustCompile(`(?s)<[^>]*>`)
	// An attribute value may hold template actions with their own quotes:
	// title="{{ tf "…" .X }}".
	attrRe  = regexp.MustCompile(`\b(?:placeholder|title|aria-label|data-confirm|alt)="((?:\{\{.*?\}\}|[^"])*)"`)
	letters = regexp.MustCompile(`[A-Za-zÄÖÜäöüß]{2,}`)
)

// textAllowed are literal texts that are the same in every language.
var textAllowed = map[string]bool{
	"kmu-buchhaltung": true, "— kmu-buchhaltung": true, "UID": true, "IBAN": true, "PDF": true, "CSV": true,
	"DATEV": true, "EXTF": true, "ATU12345678": true, "BIC": true, "ABCDATWWXXX": true, "EUR": true,
}

func templates(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("../../web/templates/*.html")
	if err != nil || len(files) == 0 {
		t.Fatalf("templates: %v", err)
	}
	out := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(f)] = string(b)
	}
	return out
}

func TestEveryMessageHasAnEnglishTranslation(t *testing.T) {
	var missing []string
	for name, src := range templates(t) {
		for _, m := range tmplMsg.FindAllStringSubmatch(src, -1) {
			if msg := unquote(m[1]); !i18n.Has("en", msg) {
				missing = append(missing, name+": "+msg)
			}
		}
	}
	gofiles, _ := filepath.Glob("*.go")
	for _, f := range gofiles {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, m := range goMsg.FindAllStringSubmatch(string(b), -1) {
			if msg := unquote(m[1]); !i18n.Has("en", msg) {
				missing = append(missing, f+": "+msg)
			}
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("no English translation: %q", m)
	}
}

func TestNoUntranslatedTextInTemplates(t *testing.T) {
	for name, src := range templates(t) {
		src = commentRe.ReplaceAllString(src, "")
		// Attributes people read.
		for _, m := range attrRe.FindAllStringSubmatch(src, -1) {
			if v := strings.TrimSpace(actionRe.ReplaceAllString(m[1], "")); letters.MatchString(v) && !textAllowed[v] {
				t.Errorf("%s: attribute text outside t: %q", name, m[0])
			}
		}
		// Text between tags. <style>/<script> and SVG <symbol> paths hold no text.
		body := regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>`).ReplaceAllString(src, "")
		body = actionRe.ReplaceAllString(body, "\x00")
		for _, part := range tagRe.Split(body, -1) {
			for _, chunk := range strings.Split(part, "\x00") {
				v := strings.TrimSpace(chunk)
				if letters.MatchString(v) && !textAllowed[v] {
					t.Errorf("%s: text outside t: %q", name, v)
				}
			}
		}
	}
}

func unquote(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, `\"`, `"`), `\\`, `\`) }
