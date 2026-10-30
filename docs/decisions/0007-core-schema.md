# ADR 0007: Core schema — money, dates, and enforced append-only entries

**Date:** 2026-08-18
**Status:** Accepted; the "Konto/Gegenkonto required on every entry" part is
superseded by [ADR 0012](0012-account-tags-optional.md)

## Context
Phase 1 writes the first domain schema: contacts, categories, and the booking
entries themselves. Three modelling choices have to be made once, correctly,
because every later phase (invoices, bank import, DATEV export) builds on them,
and because the schema must be written twice — once per dialect (ADR 0006).

ADR 0002 already decided the ledger is append-only: corrections are new
offsetting entries, never in-place edits. It left open *how* that is enforced.

## Decision

**Money is integer cents.** `amount_gross_cents`, `vat_cents`, `net_cents` are
all `BIGINT`/`INTEGER`. No floats, no `NUMERIC`, no reliance on either engine's
numeric defaults — SQLite has no real decimal type and would silently store a
float. EUR is implied; multi-currency is a non-goal.

VAT is derived from the gross amount at insert time and **stored**, not computed
on read: `vat = round(gross × rate ÷ (100 + rate))`, `net = gross − vat`, with
half-up rounding on the cent. A stored booking must never change its numbers
because a rounding helper was later edited — that is the same principle as the
append-only ledger, applied to arithmetic.

**Dates are `TEXT` in `YYYY-MM-DD` form**, not a `DATE` column. Postgres `DATE`
and SQLite's date-as-text behave differently enough through `database/sql`
(scanning, timezone assumptions) that the portable subset is a plain ISO string,
validated in Go. Booking dates are calendar facts, not instants — no timezone
belongs in them. Timestamps that *are* instants (`created_at`) stay `TIMESTAMP`
and are always written UTC.

**Append-only is enforced by the database, not by convention.** `entries` gets
triggers rejecting `UPDATE` and `DELETE` in both engines. Application discipline
alone fails the moment any code path — a future import, a bug, a manual psql
session — does the wrong thing, and the point of the ledger is that it cannot
happen.

A correction is a new row whose `reverses_entry_id` points at the original. The
reversal carries the same amounts and the opposite direction, so the pair nets
to zero. An entry is "reversed" iff some other row references it; nothing is
ever flagged on the original, because the original cannot be written to.

**Konto/Gegenkonto are required free text on every entry** (ADR 0003 needs both
to export DATEV). *(Superseded by ADR 0012: they are optional on the entry,
default from the category, and are required only by the DATEV export.)* They are *not* seeded with a chart of accounts: the correct
account framework is still an open question for the actual Steuerberater, and
seeding plausible-looking Austrian account numbers would produce exports that
look authoritative and are wrong. Categories may carry defaults once the user
configures them; until then the user types them and the UI says why.

## Consequences
+ Money arithmetic is exact and identical on both engines.
+ A booking's numbers are permanent — reproducible years later regardless of
  code changes.
+ The append-only guarantee holds against application bugs, not just against
  well-behaved code paths.
+ Reversal pairs make the DATEV export straightforward: both rows are real
  bookings, which is what the Steuerberater's system expects anyway.
- Trigger syntax differs sharply between the engines (`RAISE(ABORT, …)` vs. a
  `plpgsql` function plus trigger), so this part of the schema is genuinely
  written twice. Tested on both, so drift surfaces in CI.
- Dates as text means date-range queries are string comparisons. Correct for
  ISO-8601, but it forbids sloppy formats — hence validation in Go.
- No seeded chart of accounts means the first entries need account numbers typed
  by hand. Deliberate: correct-but-manual beats automatic-but-wrong here.

## Alternatives considered
- **`NUMERIC`/`DECIMAL` for money:** rejected — SQLite has no true decimal type,
  so the two engines would disagree.
- **Deriving VAT on read:** rejected — a stored booking's numbers must not move
  when code changes.
- **Enforcing append-only in Go only:** rejected — an invariant that matters
  belongs where it cannot be bypassed.
- **A separate `corrections` table:** rejected — a correction *is* a booking and
  must appear in every export and total as one.
