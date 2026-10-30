# CLAUDE.md

## Project
`kmu-buchhaltung` — a deliberately simple bookkeeping tool for small Austrian
businesses (EPU/KMU). Track income & outgoings, import bank transactions,
generate invoices, support Kleinunternehmerregelung and multiple VAT rates.

Guiding principle: **as simple as possible**. Prefer boring, well-understood
tech over anything clever. Every added dependency or moving part needs a
reason.

## Stack (see docs/decisions/ for full rationale)
- **Backend:** Go, stdlib `net/http` (+ `chi` router if routing gets unwieldy)
- **Database:** PostgreSQL via `pgx` for real deployments; SQLite via
  `modernc.org/sqlite` for `make dev` and tests (both pure Go, no cgo). One
  `BOOKKEEPER_DSN` picks the engine (ADR 0006). Queries stay in the portable
  subset with `?` placeholders passed through `Store.Rebind`; migrations are
  written once per dialect under `internal/store/migrations/{postgres,sqlite}/`
  and must stay in lockstep. Store money as integer cents — never rely on either
  engine's numeric defaults.
- **Frontend:** server-rendered `html/template`, mobile-first responsive CSS
  (design tokens + light/dark themes in `web/static/app.css`), one optional
  script `web/static/app.js` for progressive enhancement (ADR 0013; htmx only
  if a real partial-update need appears), plus a PWA manifest/service worker
  for an installable, app-like feel — no separate native app, no Flutter (see
  ADR 0004). No SPA framework, no JS build step, no node toolchain. No inline
  scripts or `style=` attributes — the CSP forbids them (ADR 0017).
  UI texts go through `t`/`tf` with the German text as the key; add the
  English entry to `internal/i18n/en_*.go` or the tests fail (ADR 0024).
- **PDF invoices:** `go-pdf/fpdf` with embedded DejaVu Sans; `rsc.io/qr` for the
  EPC payment QR code (ADR 0008, ADR 0025)
- **Bank import:** CSV only, by decision (ADR 0034). CAMT.053 reopens when a real
  file is at hand; EBICS (built on it) when the bank confirms access and price.
  PSD2 APIs need a licence and are not an option
- **Deployment:** single static binary, shipped as an amd64 OCI image pushed to
  `ghcr.io/chrthal/accountant` by GitHub Actions on a `v*` tag (ADR 0005).
  The container is stateless; state lives in Postgres (or a SQLite file on a
  volume).

## Build & release
- `make help` lists everything. `make dev` (SQLite, no container) is the inner
  loop; `make test` is host tests; `make test-postgres` runs both engines like
  CI does; `make up` starts the production-shaped app + Postgres via compose.
- The Makefile auto-detects `docker` or `podman`; the Dockerfile and compose
  files deliberately avoid anything specific to either (ADR 0005).
- Release: move `[Unreleased]` in `CHANGELOG.md` to a `## [X.Y.Z] - date`
  section, commit, tag `vX.Y.Z` → CI tests, takes the release notes from that
  section (and stops if there is none), builds, asserts the image is amd64
  and reports the stamped version, pushes `:<version>` and `:latest`, opens a
  GitHub release. Pushes to `main` publish `:edge`. User-facing changes go
  under `[Unreleased]` as they land.
- **CI:** GitHub Actions on `ubuntu-latest`; Postgres is a service
  container, the OCR tests run in a container with tesseract and poppler.
- A phase is not done until it passes its acceptance criterion in
  `plans/0001-mvp-scope.md`, not when CI is green.
- Backups: `bookkeeper backup|restore|verify`, daily via
  `BOOKKEEPER_BACKUP_DIR` (ADR 0019). **A new table or column must be added to
  `backupTables` in `internal/store/backup.go`.** `CheckBackupCoverage` fails
  the tests, and backups in production, until it is. A migration that writes
  rows (INSERT/UPDATE/DELETE) must be listed in `dataMigrations` in
  `internal/store/restore_running.go`; the browser restore (ADR 0030) depends
  on it, and a test enforces it.
- Bank assistance (ADR 0020): rules first (`internal/match`, text split,
  "wie zuletzt"); an optional local LLM via `BOOKKEEPER_LLM_URL` (Ollama) only
  for what the rules leave empty. Its output is validated and only ever a
  draft; nothing books itself.
- Optional email reminders to the owner (ADR 0039, 0040): never to
  customers. The mail server is set in the settings (password write-only,
  table `mail_server` left out of backups) or by `BOOKKEEPER_SMTP_*`, which
  wins.
- Optional login via `BOOKKEEPER_PASSWORD` / `BOOKKEEPER_PASSWORD_FILE`
  (ADR 0017); cross-origin POSTs are refused and the CSP forbids inline code.
- Receipt OCR shells out to `tesseract`, PDF invoices to poppler's
  `pdftotext`/`pdftoppm`; all optional — the app must keep working without them
  (ADR 0011, ADR 0018). `go run ./tools/icons` regenerates the PWA
  icons; their PNGs are committed, since there is no build step.

## Domain rules — Austria, current as of 2026
- **Kleinunternehmerregelung** (§ 6 Abs 1 Z 27 UStG): revenue threshold
  **€55,000 gross/year** (raised from €35,000 net, effective since 1.1.2025).
  10% tolerance band up to **€60,500**: exceed by ≤10% → exemption still
  applies through year-end, drops the following year; exceed by >10% →
  exemption lapses immediately, retroactive to the transaction that crossed
  the line. No VAT charged, no Vorsteuerabzug. Invoices must carry a note
  citing the exemption instead of a VAT line.
- **VAT rates:** invoices need a **per-line-item** selectable VAT rate —
  20% (Normalsatz), 13%, 10% (reduced rates). Verify the exact rate set
  against current BMF guidance before hardcoding, since reduced-rate
  categories shift occasionally.
- **Invoice mandatory fields** (§ 11 UStG): issuer, recipient, date,
  quantity/description, net amount, VAT rate & amount (or the
  Kleinunternehmer exemption note in its place), sequential invoice number,
  UID number where applicable.
- This is not tax advice — flag anywhere the logic is uncertain rather than
  guessing at edge cases (EU cross-border Kleinunternehmer rules, mixed
  VAT-rate invoices, threshold-crossing mid-year) instead of shipping a
  plausible-looking but wrong calculation.

## Working conventions
- **Plans live in `plans/`.** Before starting a session, read the current
  plan file (`plans/0002-post-mvp.md` since the MVP). Before ending one, update it with what's done and what's next.
- **Decisions live in `docs/decisions/`** as ADRs (`NNNN-title.md`: Context /
  Decision / Consequences / Alternatives considered). Anything non-trivial —
  schema changes, library choices, invoice-numbering scheme, VAT edge-case
  handling — gets one. `docs/decisions/README.md` is the index; update it,
  including the status of anything superseded.
- The split is: `plans/` is what we're going to do, `docs/decisions/` is why we
  decided it. A superseded ADR stays in place — the record of a reversed
  decision is part of the history.
- **Commit `CLAUDE.md`, `docs/`, and `plans/` to git** alongside the code.
  This is the project's persistent, shared memory — not chat-local, not
  machine-local auto-memory.
- Work one phase/feature at a time (see `plans/0001-mvp-scope.md`). Mark a
  phase done only after it actually runs end-to-end, not when the code compiles.

## Non-goals for MVP
No multi-currency, no payroll, no full double-entry Bilanzierung as the app's
own internal logic (this is cash-basis Einnahmen-Ausgaben-Rechnung; the
Konto/Gegenkonto tags exist only to support DATEV export — optional per entry,
defaulted per category, required only by the export itself — see ADR 0003 and
ADR 0012), no multi-tenant/SaaS auth — single user or small team, self-hosted.
No cloud OCR (see ADR 0002). No native mobile app / Flutter (see ADR 0004).

## Open questions
Track here until resolved, then promote the resolution to an ADR.
- Chart-of-accounts / Konto-Gegenkonto mapping and booking period for the
  DATEV export — confirm with the actual Steuerberater (ADR 0003).
