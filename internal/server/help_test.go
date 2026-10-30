package server_test

import (
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	sectionIDRe = regexp.MustCompile(`<section[^>]* id="([^"]+)"`)
	anchorRe    = regexp.MustCompile(`href="#([^"]+)"`)
	pathLinkRe  = regexp.MustCompile(`href="(/[^"#]*)(#[^"]*)?"`)
)

// The help exists in German and English with the same sections in the same
// order, and every in-page link points at one of them.
func TestHelpLanguagesMatch(t *testing.T) {
	ids := map[string][]string{}
	for _, lang := range []string{"de", "en"} {
		b, err := os.ReadFile("../../web/templates/help/" + lang + ".html")
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range sectionIDRe.FindAllStringSubmatch(string(b), -1) {
			ids[lang] = append(ids[lang], m[1])
		}
		for _, m := range anchorRe.FindAllStringSubmatch(string(b), -1) {
			if !slices.Contains(ids[lang], m[1]) {
				t.Errorf("%s: link to #%s, but there is no such section", lang, m[1])
			}
		}
	}
	if len(ids["de"]) < 10 || !slices.Equal(ids["de"], ids["en"]) {
		t.Errorf("sections differ:\nde %v\nen %v", ids["de"], ids["en"])
	}
}

// Every page the help links to exists, in both languages.
func TestHelpLinksWork(t *testing.T) {
	h, _ := newTestServer(t)
	configureIssuer(t, h, false)
	for _, lang := range []string{"de", "en"} {
		post(t, h, "/settings", url.Values{"issuer_name": {"Mustermann IT"}, "language": {lang}})
		body := get(t, h, "/help").Body.String()
		if !strings.Contains(body, `id="sicherung"`) {
			t.Fatalf("%s: the help page has no content", lang)
		}
		seen := map[string]bool{}
		for _, m := range pathLinkRe.FindAllStringSubmatch(body, -1) {
			if seen[m[1]] || strings.HasPrefix(m[1], "/static/") {
				continue
			}
			seen[m[1]] = true
			if rec := get(t, h, m[1]); rec.Code != 200 {
				t.Errorf("%s: help links to %s, which answers %d", lang, m[1], rec.Code)
			}
		}
	}
}
