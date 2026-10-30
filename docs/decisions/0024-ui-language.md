# 0024 — UI language: German source texts, English catalog

## Context
The user asked for a language setting next to the date format. The UI was
written in German throughout: templates, flash notices, validation messages.
The project has no build step and no JS toolchain, so gettext tooling
(`.po` files, `xgettext`) or a code generator would be a new moving part.

## Decision
- The German text is the message id. Templates call `{{ t "…" }}` and
  `{{ tf "… %s" arg }}`; Go code calls `s.T` / `s.Tf`. German needs no
  catalog, so the German UI is exactly what it was.
- `internal/i18n` holds plain Go maps per language (`en_*.go`, split by
  area). A missing translation falls back to the German text.
- Store errors are written in English in Go code. A small `de` catalog
  (`de_store.go`) translates the known ones for the German UI; `errText`
  picks the text.
- The language is a setting (`language`: `de` or `en`), loaded into
  `Server.prefs` with the date format, so a request needs no extra query.
- Amounts follow the language: `eur` uses `money.FormatLang`
  (`1.234,56` / `1,234.56`). `money.Parse` already accepts both notations.
- Guard tests: every `t`/`tf` string in templates and Go code must have an
  English entry; template text nodes and the attributes `placeholder`,
  `title`, `aria-label`, `data-confirm`, `alt` must not carry untranslated
  text; the English pages are loaded and searched for German words.

## Consequences
- Invoices and their PDFs stay German. They are legal documents for
  Austrian recipients (§ 11 UStG), not UI.
- Some generated texts stay German: the reasons from `match.CategoryFor`
  and the skipped-row messages of the CSV import. Dynamic store errors not
  in the `de` catalog show in English in the German UI.
- A new UI text needs an English entry, or the tests fail.

## Alternatives considered
- `golang.org/x/text/message` with catalogs: a new dependency and a
  generator step for the same lookup.
- Short keys (`nav.entries`): templates become unreadable, and the German
  text would have to move into a catalog too.
- Per-request `Accept-Language`: single user or small team; a setting is
  simpler and predictable.
