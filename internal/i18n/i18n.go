// Package i18n translates the user interface (ADR 0024).
//
// The source text is the German the app was written in: templates and
// handlers keep their German strings, wrapped in t/T, and an English
// catalog maps each one. Store errors are written in English, so for those
// the direction is reversed and a German catalog maps them. A message that
// has no entry is shown as written, so a missing translation degrades to the
// source language instead of to a blank.
package i18n

import (
	"fmt"
	"sort"
)

// Languages the user can choose, by code, with their own name.
var Languages = map[string]string{
	"de": "Deutsch",
	"en": "English",
}

// Default is the language of a fresh installation.
const Default = "de"

var catalogs = map[string]map[string]string{}

// register adds translations for lang; the per-area catalog files call it
// from init.
func register(lang string, m map[string]string) {
	c := catalogs[lang]
	if c == nil {
		c = map[string]string{}
		catalogs[lang] = c
	}
	for k, v := range m {
		c[k] = v
	}
}

// T translates msg into lang.
func T(lang, msg string) string {
	if v, ok := catalogs[lang][msg]; ok {
		return v
	}
	return msg
}

// Tf translates a format string and fills it in. The translation must keep
// the verbs in the same order.
func Tf(lang, msg string, args ...any) string {
	return fmt.Sprintf(T(lang, msg), args...)
}

// Has reports whether lang has an entry for msg, for the completeness test.
func Has(lang, msg string) bool {
	_, ok := catalogs[lang][msg]
	return ok
}

// Codes returns the language codes, sorted, for the settings page.
func Codes() []string {
	out := make([]string, 0, len(Languages))
	for c := range Languages {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
