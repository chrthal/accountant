# Plan 0001: MVP scope

**Status:** All six phases built. Phases 0–3 and 5 done; Phase 4 waits on outside input (a DATEV Prüfprogramm run)
**Date:** 2026-08-18

## Goal
Minimal usable bookkeeping tool for one small Austrian business (EPU/KMU):

1. Contacts (customers / vendors)
2. Manual income & outgoing entry, with categories
3. Bank transaction import (CSV) with manual matching to entries
4. VAT handling: per-line-item rate (20/13/10%), Kleinunternehmer mode toggle
   (no VAT, exemption note on invoice instead)
5. Invoice generation as PDF, sequential invoice numbering
6. Kleinunternehmer threshold tracker: running yearly gross revenue vs.
   €55,000 (and the €60,500 tolerance ceiling)
7. Receipt/Beleg OCR: scan or upload a receipt, extract amount/date/vendor,
   suggest a category — reduces manual entry for outgoings
8. Append-only booking ledger: entries are never edited in place; corrections
   are new offsetting entries, so the record stays traceable (see ADR 0002)
9. Steuerberater export: structured CSV covering income/outgoings + VAT
   summary (human-readable fallback), plus a DATEV EXTF Buchungsstapel export
   for direct import into the Steuerberater's DATEV system (ADR 0003)
10. Mobile support via a responsive, mobile-first web UI + PWA manifest
    (installable, "add to home screen") — no native app (ADR 0004)

## Explicit non-goals for MVP
- CAMT.053 import (later stretch goal)
- Multi-user / auth complexity
- Full double-entry Bilanzierung as the app's own internal accounting logic
- Cloud OCR APIs — OCR runs locally (see ADR 0002)
- Native mobile app / Flutter (see ADR 0004)
- Offline-first data entry with sync (PWA covers installability and cached
  UI shell only, not offline bookkeeping)

## Phases

Each phase carries an **acceptance criterion**: a thing a person does, not code
that exists. Per CLAUDE.md a phase is done only when that actually happens
end-to-end — not when it compiles.

### Phase 0 — build & release foundation
Repo skeleton (`cmd/`, `internal/server`, `internal/store`, `web/`), `/healthz`
backed by a real `SELECT 1`, `-version` flag stamped at build time, two database
engines behind one DSN (ADR 0006), container image, local compose environment,
CI + release pipeline (ADR 0005).

**Done when:** `make test-postgres` passes on both engines; `make test-container`
passes; `make up` serves `localhost:8080/healthz`; a pushed version tag yields a
pullable image whose `-version` prints that version.

**Status:** done.

### Phase 1 — core data model
Contacts, transactions, categories, manual entry. **Append-only ledger semantics
from the start** (corrections = new offsetting entries, not edits), enforced by
database triggers on both engines (ADR 0007). Mobile-first responsive templates
from the very first screen.

**Done when:** a contact plus one income and one outgoing entry can be created
through the browser at phone width; a correction produces a second, offsetting
row with the original untouched.

**Status:** done. Account tags (Konto/Gegenkonto) were first required on every
entry and later made optional, defaulted per category (ADR 0012).

### Phase 2 — invoices & VAT
PDF invoice generation, sequential numbering, per-line-item VAT rates,
Kleinunternehmer mode (ADR 0008).

**Done when:** a mixed-rate invoice (20% and 10% lines) renders to a PDF
carrying every § 11 UStG mandatory field, and toggling Kleinunternehmer mode
replaces the VAT block with the exemption note.

**Status:** done. Reverse charge, innergemeinschaftliche Lieferungen and
cross-border Kleinunternehmer rules are deliberately out of scope: each changes
the mandatory fields and the required wording.

### Phase 3 — bank CSV import
Import + reconciliation/matching against existing entries. One generic CSV
importer with a column mapping instead of per-bank parsers (ADR 0009).

**Done when:** a real CSV export from a bank imports cleanly, and a
transaction can be matched to an entry and unmatched again.

**Status:** done, checked against a real meinELBA export. It showed two rough
edges that synthetic files did not: no header row, and the counterparty
embedded in the text field. Both were fixed later (ADR 0014, ADR 0020).

### Phase 4 — reporting & exports
Threshold tracker, plain CSV export, DATEV EXTF Buchungsstapel export
(ADR 0010).

**Done when:** the tracker shows correct running gross revenue against €55,000
and the €60,500 ceiling, and a generated DATEV file passes DATEV's own
**DatevFormatPrüfProgramm** — format-plausible is explicitly not enough
(ADR 0003).

**Status:** built, criterion **not met**. The Prüfprogramm is Windows software
that needs a DATEV installation; someone with one has to run it on a generated
file once. The reports page says so.

### Phase 5 — receipt OCR & PWA
Local Tesseract extraction (amount, date, vendor) feeding a pre-filled outgoing
draft for review; PWA manifest + service worker (ADR 0011).

**Done when:** a phone-camera photo of a receipt produces a draft the user
confirms into a booking, and the app installs to the home screen.

**Status:** done, verified through the same multipart upload a phone makes.

## Still open
- **A DATEV Prüfprogramm run** — Phase 4's criterion.
- **The Steuerberater's confirmation** of the chart of accounts, the booking
  period and the Soll/Haben orientation for the DATEV export. Since ADR 0012
  this does not block day-to-day booking.

Work after the MVP continues in [plan 0002](0002-post-mvp.md).
