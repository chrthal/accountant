# ADR 0008: Invoices — numbering, issuer data, and PDF rendering

**Date:** 2026-08-18
**Status:** Accepted

## Context
Phase 2 issues invoices. Three things have to be settled before code: how
invoices are numbered (an open question since the project started), where the
issuer's own details come from, and which library renders the PDF.

§ 11 UStG fixes most of the content: issuer, recipient, issue date, quantity and
description, net amount, VAT rate and amount — or, for a Kleinunternehmer, a note
citing the exemption in place of the VAT line — a sequential invoice number, and
the UID number where applicable.

## Decision

**Numbering: `YYYY-NNNN`, reset each year, gapless within the year.**
§ 11 Abs 1 Z 6 UStG requires a *fortlaufende* number that identifies the invoice
uniquely; it does not require a single unbroken lifetime sequence, and a
year-prefixed series is standard Austrian practice. The year is stored as its own
column alongside the sequence, so the rendered string is derived, never parsed.

The next number is `MAX(seq) + 1` for the year, taken inside the same transaction
that inserts the invoice, with a `UNIQUE (year, seq)` constraint behind it. If
two invoices are ever created concurrently, the loser hits the constraint and
retries rather than duplicating a number. This is deliberately simpler than a
counter table: the invoices themselves are the counter, so a number can never
drift from what was actually issued.

**Issuer data lives in a `settings` table and is snapshotted onto each invoice.**
An invoice must show the issuer as they were when it was issued. Moving office or
registering for VAT must not silently rewrite last year's invoices, so name,
address, UID and the Kleinunternehmer flag are copied onto the invoice row at
creation. Same reasoning as the append-only ledger (ADR 0007), applied to the
document.

**Kleinunternehmer mode is a per-invoice snapshot, not a live setting.** When it
is on, every line is 0% and the invoice carries the exemption note instead of a
VAT block. Mixing exempt and taxed lines on one invoice is refused rather than
guessed at.

**PDF: `github.com/go-pdf/fpdf`** — the maintained fork of the archived
`jung-kurt/gofpdf`. Pure Go, no cgo (so ADR 0005's static binary survives), no
external binary like wkhtmltopdf, and a small enough API to keep the renderer
readable. maroto sits on top of the same engine and adds a layout DSL that a
single invoice template does not need.

fpdf's core fonts are single-byte, so text goes through its cp1252 translator —
which covers German umlauts, ß and €. Anything outside cp1252 would need an
embedded TTF; not a Phase 2 problem, but it is why the translator is applied in
one place rather than scattered.

**The document model is separated from the PDF writer.** `invoice.Document` is
built and totalled in plain Go and tested exhaustively; the fpdf code only draws
it. PDF bytes are near-impossible to assert on meaningfully, so the logic worth
testing lives where it can be.

## Consequences
+ Invoice numbers are gapless per year and cannot collide.
+ Historical invoices stay faithful to the day they were issued.
+ VAT totals are grouped per rate, which is what § 11 requires on a mixed-rate
  invoice and what the DATEV export will want later.
- A yearly reset means the number alone is not unique across years; every
  reference must carry the year, which the `YYYY-NNNN` format enforces.
- Snapshotting the issuer duplicates data. That is the point, but it does mean a
  typo in the address is fixed for future invoices only — a corrected invoice is
  a new document, as it would be on paper.
- fpdf's cp1252 limit will need an embedded font if a customer name ever contains
  characters outside it (Cyrillic, Greek, some Eastern European letters). It will
  render as `?` rather than fail, so the renderer must not be trusted blindly for
  non-Western names.

## Explicitly out of scope, and why
Reverse charge (§ 19 UStG), innergemeinschaftliche Lieferungen, and the
cross-border Kleinunternehmer rules each change the mandatory fields and the
required wording. This is not tax advice and guessing at them would produce
invoices that look right and are not. Phase 2 covers **domestic Austrian
invoices** — plain VAT rates, or the Kleinunternehmer exemption. Anything else
needs a real requirement and its own ADR.

## Alternatives considered
- **Continuous lifetime numbering (no yearly reset):** equally legal, marginally
  simpler. Rejected because year-scoped numbers are what Austrian
  Steuerberater and customers expect to see, and they make archiving obvious.
- **A counter table for numbering:** rejected — a second source of truth that can
  disagree with the invoices it counts.
- **maroto:** a layout DSL over the same engine; unnecessary for one document.
- **HTML → PDF via a headless browser:** rejected outright — a browser as a
  runtime dependency contradicts the single-static-binary constraint.
