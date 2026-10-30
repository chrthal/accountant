# ADR 0014: Bank matching: one transfer, several bookings; guessed and remembered mappings

**Date:** 2026-09-29
**Status:** Accepted. Extends ADR 0009.

## Context
Three things from plan 0001's Phase 3 and plan 0002's gap analysis:

1. **A transfer can be several bookings.** ADR 0015 books the payment of a
   mixed-rate invoice as one entry per VAT rate, but the customer pays it with
   one transfer. `bank_transactions.matched_entry_id` (one entry, UNIQUE) could
   not express that, so such a payment could never be reconciled.
2. **The `has_header` default was wrong for the real bank.** The meinELBA
   export has no header row, and with the box checked by default its first
   transaction was silently used as column labels.
3. **Mappings were forgotten.** Every import of the same bank's export needed
   the same four choices again.

## Decision

### `bank_matches` link table
`bank_matches (bank_transaction_id, entry_id UNIQUE)` replaces the column.
Rules, enforced in `MatchBankTransaction`:

- an entry is matched at most once (the database enforces this with UNIQUE),
  so no payment counts twice;
- all entries share the transaction's direction;
- their gross amounts **sum** to the transaction amount, to the cent.

Migration 0007 copies existing matches over and clears the old column. The
column itself stays, unused, because dropping a UNIQUE, referenced column on
SQLite needs a full table rebuild (as in 0005), which is not worth it here.

### Match suggestions and one-step actions
For each open transaction the bank page offers:

- **suggested bookings**: effective, unmatched, same direction and exact
  amount, nearest date first, at most five. One click matches.
- **"Rechnung … bezahlt"**: for incoming transfers, open invoices whose
  gross equals the amount. One click books the payment (ADR 0015) on the
  **transfer date** and matches all its entries. Everything is checked before
  anything is booked. If only the final match fails, the page says the
  payment is booked and the link must be made by hand.
- **"Buchung anlegen"**: opens the booking form prefilled from the line
  (date, direction, amount, counterparty or purpose). Saving matches it. If
  the user changed the amount so it no longer matches, the booking is still
  saved and its page says the match was refused.
- the full list of candidates stays available as a fallback, now filtered to
  the same direction.

Reversed bookings and their Stornos are no longer candidates: together they
net to zero, and no money moved for them.

### Guessed and remembered column mappings
`bankcsv.File.Guess` proposes a mapping **from the values**:

- the first column whose values read as dates (the booking date, which comes
  before Valuta in Austrian exports);
- the column whose values read as signed amounts *with a decimal separator*
  (so dates and times do not qualify);
- **a header exists iff the first row does not read as a date in that
  column**;
- counterparty and purpose come from header names when there is a header,
  otherwise the purpose is the longest text column.

A successful import stores its mapping under the file's **layout signature**
(delimiter, column count, and the header row if any) in `settings` as
`bank_mapping:<signature>`. The next upload with that signature is preselected
with it and says so ("wie beim letzten Import").

The guess is never imported unseen. The mapping page always shows the preview
and asks for confirmation, as ADR 0009 requires.

## Consequences
- The meinELBA export imports with no changes to the form: no header, date
  in column 1, amount in column 4.
- Two headerless exports with the same column count share a signature, since
  nothing else tells them apart. The user still confirms, so the worst case is
  a preselection they change.
- Matching is still housekeeping, not bookkeeping: unmatching deletes the
  links and never touches an entry.

## Alternatives considered
- **Per-bank parsers:** rejected again for ADR 0009's reason. They are
  untestable without each bank's real file.
- **Allow a transaction to match a sum *less* than its amount (partial
  reconciliation):** it would hide a missing booking. The sum must be exact.
- **Split a transaction into several bank rows:** it would invent bank data
  that the bank never sent.
