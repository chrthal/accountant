# ADR 0003: DATEV Buchungsstapel export

**Date:** 2026-08-17
**Status:** Accepted — supersedes ADR 0002, decision 3

## Context
ADR 0002 chose plain CSV over DATEV export, reasoning DATEV format complexity
wasn't worth building speculatively. Revisited: DATEV support is now an
explicit requirement, not speculative.

DATEV's EXTF Buchungsstapel format is a semicolon-separated CSV: a multi-line
header (identifies format/version/category/date range/currency), a column
header row, then one row per booking. Each booking row needs at minimum:
amount, Soll/Haben-Kennzeichen (debit/credit indicator), Konto (account),
Gegenkonto (offsetting account), Belegdatum, and a Buchungstext. It's a file
format, not an API or hosted service — generating it locally doesn't violate
the no-third-party-services constraint.

## Decision
Add a DATEV EXTF Buchungsstapel exporter as a first-class export option
alongside the plain CSV export.

To produce it, every income/outgoing entry needs an associated **Konto** and
**Gegenkonto** — this means the data model needs a lightweight chart-of-accounts
mapping (not full double-entry bookkeeping in the app's own logic, just enough
tagging per entry to populate those two DATEV fields correctly on export). This
should reuse whatever account framework the user's actual Steuerberater expects
— Austrian Steuerberater/DATEV setups vary, so the account numbers themselves
should be a configurable mapping, not hardcoded.

## Consequences
+ Steuerberater can import directly into DATEV, no manual reformatting
+ CSV export path from ADR 0002 stays as the simpler fallback/human-readable option
- Adds a real modeling requirement (Konto/Gegenkonto per entry) that touches
  the core schema — should land in Phase 1 alongside the append-only ledger,
  not bolted on later
- DATEV format has strict validation (DatevFormatPrüfProgramm); needs testing
  against a real sample file before considering this done, not just "looks
  plausible"

## Open item
Confirm with the actual Steuerberater which chart-of-accounts framework and
booking period (monthly/quarterly/yearly) they expect — needed before the
Konto/Gegenkonto mapping can be finalized, not before building the export
mechanism itself.
