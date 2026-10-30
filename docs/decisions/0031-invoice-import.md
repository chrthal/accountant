# 0031 — Importing invoices from another tool

## Context
Someone switching to this app brings the invoices of the current year,
written with another program. Without them, open items, payments, the
year's revenue (and with it the Kleinunternehmer threshold, ADR 0022) and
the year archive (ADR 0029) are incomplete, and new invoice numbers start
over where the old program left off. The user asked for an import under
Rechnungen to make migration easier.

## Decision
- **Upload the original PDF, confirm, store.** The PDF is read like a Beleg
  (ADR 0011, 0018): number, date, total and the recipient's name are
  pre-filled, and an existing contact with a similar name is chosen. The
  invoice form opens in import mode with the number field, one position
  with the total split by the rate (0 % when the exemption applied on that
  date), and a "new customer" field that creates the contact on save.
  Nothing is stored before the user confirms.
- **An imported invoice is an issued invoice** (`imported = 1`): immutable,
  with its lines and totals as `invoice.Build` computes them, so open
  items, payments, bank matching, reports and the Kleinunternehmer tracker
  count it. The exemption follows the invoice's own date (ADR 0022).
- **The original PDF is its document**, in `invoice_originals`, append-only
  like Belege. The PDF link and the year archive serve the original, never
  a re-rendered page.
- **Numbering continues** (ADR 0025): a number that fits the configured
  scheme and prefix (`E260112001`, `2026-0007`) takes its seq, so the next
  invoice this app issues follows it. A number that fits no scheme is
  counted from seq 2,000,000, which numbering never reads. New numbers skip
  any number already taken, so an imported number is never issued again.
- PDF only, up to 10 MiB, number unique, 1–40 characters.

## Consequences
- The positions are what the user confirms, not a parse of the PDF's
  table. One position with the total is enough for the figures; the
  original remains the document of record.
- An imported invoice carries today's issuer settings as its snapshot; its
  printed content is the original PDF.
- Payments of imported invoices are booked as for any invoice, from the
  invoice page or from a bank line.

## Alternatives considered
- **Parsing the line items out of the PDF**: every program lays them out
  differently; a wrong amount in the books is worse than a typed one.
- **Archiving the PDF as a plain file without an invoice**: open items,
  revenue and numbering would stay wrong.
- **CSV import of invoice lists**: no common format between tools, and the
  originals would still be missing.
