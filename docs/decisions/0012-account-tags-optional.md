# ADR 0012: Konto/Gegenkonto move from required-per-entry to category defaults

**Date:** 2026-08-18
**Status:** Accepted — supersedes ADR 0007's "required free text on every entry"

## Context
ADR 0007 made `konto` and `gegenkonto` required on every booking, enforced by
`CHECK (konto <> '')` in both dialects and by `NewEntry.Validate`. The entry form
asks for both as free-text numeric fields on every single booking.

The question that prompted this: **does a small Austrian business need Soll and
Haben at all?**

Legally, no. An EPU or small KMU doing Einnahmen-Ausgaben-Rechnung (§ 4 Abs 3
EStG) is on cash basis and is not required to keep double-entry books;
Doppelte Buchführung follows from the § 189 UGB Rechnungslegungspflicht
thresholds, which this user is far below. CLAUDE.md already makes full
Bilanzierung a non-goal. Nothing in the app's own logic — totals, the
Kleinunternehmer tracker, invoices, the CSV export — reads these fields.

Exactly one consumer does: the DATEV EXTF Buchungsstapel (ADR 0003), which is a
double-entry format and needs an account, a contra-account and an S/H indicator
per row.

Two things were conflated in ADR 0007, and separating them is most of this
decision:

- **The Soll/Haben indicator is already derived, not asked.** `export/datev.go`
  computes `S`/`H` from the entry's direction. For cash-basis bookkeeping that
  is fully determined, and the user never types it. This was never a burden.
- **The account numbers are asked, on every entry, and that is the burden.** It
  requires a sole trader to know that office supplies books to 7600 against 2700
  — an accounting decision, at booking time, in service of an export that may
  never be used, since whether the Steuerberater wants a DATEV import at all is
  still open.

The failure mode this creates is worse than inconvenience: a user who must fill
a required field they do not understand will type *something*. Invented account
numbers are indistinguishable from correct ones in the export, which is the same
class of error ADR 0003 refuses to make with the Berater-Nummer.

## Decision

**Konto and Gegenkonto become optional on an entry.** The `CHECK (konto <> '')`
constraints are dropped; the columns stay `NOT NULL DEFAULT ''` so the shape of
the row does not change and the exporters keep reading plain strings.

**Categories supply them.** `categories.default_konto` / `default_gegenkonto`
already exist (ADR 0007 anticipated this) and were unused. `CreateEntry` now
resolves a blank account from the entry's category at insert time, and the
resolved value is **stored on the row**, not looked up on read — the same
principle as the stored VAT split: a booking's numbers must not move because a
category was later edited.

The per-entry fields remain as an override, no longer required, below the
category selector rather than above it.

**The DATEV export enforces what it needs, at the point it needs it.**
`export.DATEV` refuses to write a file if any booking in range is missing an
account, naming the offending entries. The reports page surfaces that as a list
the user can act on. This is the same posture the exporter already takes on the
Berater-Nummer: the requirement belongs to the export, not to the ledger.

**Soll/Haben stays derived and stays out of the UI.** It is not a field, not a
setting, and not a question the user is asked.

## Consequences
+ A booking needs a date, a description, an amount and a VAT rate. That is what
  cash-basis bookkeeping actually requires.
+ Account numbers are configured once per category, by whoever knows the chart
  of accounts, instead of retyped per booking by whoever does not.
+ Entries booked before the chart of accounts is settled are no longer blocked;
  they can be given accounts later by their category, for future bookings.
- **Existing entries keep the accounts they were given.** Resolution happens at
  insert. An entry booked with a guessed number keeps it, and the fix is a
  Storno plus a fresh booking — the append-only rule applies here as everywhere.
- A year containing entries without accounts cannot be exported to DATEV until
  they are dealt with. Deliberate: a partial Buchungsstapel is worse than a
  refused one.
- The SQLite migration is a full table rebuild, since SQLite cannot drop a
  CHECK constraint. Done inside the migration transaction with
  `defer_foreign_keys`, and covered by a test that seeds data at the pre-0005
  schema and asserts it survives.

## Alternatives considered
- **Leave it required and seed an Austrian chart of accounts** (EKR/SKR04):
  rejected for the reason ADR 0007 gave — plausible-looking account numbers
  produce exports that look authoritative and are wrong.
- **Drop the fields entirely and abandon the DATEV export:** rejected as
  premature. The Steuerberater conversation has not happened; if it lands on
  "send me a CSV", this becomes the right answer and the columns can go then.
- **Resolve the category default on read instead of storing it:** rejected —
  editing a category would silently rewrite the account numbers of bookings
  already handed to the Steuerberater.
- **Ask for Soll/Haben in the UI:** rejected. It is derivable from the entry
  direction on cash basis, and asking would import double-entry thinking into a
  tool whose whole premise is that its user does not have to do that.
