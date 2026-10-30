# ADR 0009: Bank import — generic mapped CSV, not per-bank parsers

**Date:** 2026-08-18
**Status:** Accepted

## Context
Phase 3 imports bank transactions. CLAUDE.md has carried an open question since
the start: *which bank's format?* — and it is still open, because answering it
needs a real export file from the account that will actually be used.

Building a parser for a guessed bank format would be the wrong kind of work:
Austrian banks each export their own column sets, and a parser written against
an imagined layout is untestable and probably wrong. But the *mechanism* — read
a CSV, understand its columns, store rows once, match them to bookings — is the
same regardless of which bank produced the file.

## Decision

**One generic CSV importer with user-supplied column mapping.** No per-bank
parsers, no bank auto-detection. The user uploads a file, the app shows the
first rows, and the user says which column holds the date, the amount, the
counterparty and the purpose. That mapping is what makes it work with *their*
bank rather than a bank we guessed at.

The importer handles the things that genuinely vary between Austrian exports:

- **Delimiter**: `;` (the German-locale default) or `,`, detected from the header.
- **Encoding**: UTF-8, or cp1252/latin-1 — still common in bank exports, and the
  giveaway is that the bytes are not valid UTF-8.
- **Amounts**: `-1.234,56` and `-1234.56` both parse, via the existing
  `money.Parse` heuristics.
- **Dates**: `DD.MM.YYYY` and `YYYY-MM-DD`, the two that actually turn up.

**Rows are deduplicated by a hash of their raw content.** Re-importing the same
file, or a file whose date range overlaps a previous one, must not double a
transaction. The hash is over the raw line plus the mapped date and amount, and
it is `UNIQUE`, so idempotency is enforced by the database rather than by
remembering which files were imported.

**A bank transaction is not a booking.** It is evidence that money moved. It
lands in its own table, and matching it to a ledger entry is a link that can be
made and unmade — unlike entries and invoices, which are append-only. Nothing
about a transaction's own content is editable; only its link to an entry is.

This distinction is what keeps the append-only guarantee honest: reconciling is
bookkeeping housekeeping, not a change to the books.

## Consequences
+ Works with any bank that exports CSV, today, without a format decision.
+ Re-importing is safe, which is what people actually do with bank exports.
+ The matching link stays correctable without touching the ledger.
- The user has to map columns once per file layout. Remembering a mapping per
  account is an obvious later improvement, deliberately not built yet.
- Without a real export to test against, the parser is verified with synthetic
  files covering the variations above. **The Phase 3 acceptance criterion — "a
  real CSV export from the user's actual bank imports cleanly" — is therefore
  not met yet.** It needs one real file.
- CAMT.053 XML remains out of scope (a stretch goal in the MVP plan). The
  mapping UI would not apply to it; it is a structured format with a schema, so
  it would be its own parser and its own ADR.

## Alternatives considered
- **A parser per bank (Erste, Raiffeisen, BAWAG …):** rejected as speculative —
  each needs a real sample to be written correctly, and there is none.
- **Auto-detecting the layout by column headers:** a heuristic that fails
  silently on the file it does not know. The mapping step is one extra click and
  never guesses wrong.
- **Storing the uploaded file server-side between the two steps:** avoided;
  the file is carried through the mapping form instead, so an abandoned import
  leaves nothing behind. This caps practical file size, which is fine for a bank
  export covering a year.
