# 0032 — Bulk import of invoices

## Context
The single import (ADR 0031) takes one PDF per upload and a full form per
invoice. Someone moving over mid-year brings dozens of invoices. The user
asked for a bulk import.

## Decision
- **Upload many PDFs at once** (`POST /invoices/import/batch`). Each file is
  read exactly as in the single import (one shared function) and put on a
  **waiting list**, `invoice_import_queue`: the original and what was read,
  as text. Files that are not PDFs are skipped and counted. At most 100
  invoices wait at a time. The request is read file by file, never as a
  whole.
- **Review in one list**, like the bank booking suggestions (ADR 0020):
  number, date, gross amount, VAT rate, recipient (an existing contact or a
  new customer's name), and a link to the original. A row is ticked in
  advance only when it is complete; otherwise it names why not: no number,
  a number that exists already or twice in the list, no date, no amount,
  no recipient.
- **"Angehakte importieren"** imports each ticked row on its own, through
  the same `ImportInvoice` as a single import, with one position holding
  the total. The waiting-list row is removed in the same transaction. A
  failing row stays with its error; every edit is saved to the list before
  importing, so nothing typed is lost. A new customer's name creates the
  contact once per submit, however many rows name it.
- **"Einzeln öffnen"** opens a row in the full import form, for an invoice
  whose positions matter; the original stays on the waiting list instead
  of travelling through the page.
- The waiting list is not part of the books and is left out of backups
  (`derivedTables`): the originals are still on the user's disk.

## Consequences
- Checked in the real image: two PDFs, an invoice from another tool and an
  app-rendered invoice, were read correctly (number, date, amount, recipient, 0 % under
  the exemption); their equal numbers were flagged; after correcting one,
  both were imported and the original came back byte for byte.
- Reading scanned PDFs (OCR) takes seconds each, so a large batch of scans
  makes one long upload request.
- A restore (ADR 0030) does not bring back a waiting list.

## Alternatives considered
- **Keeping the uploads in the page** (as the single import does): dozens
  of PDFs as form fields on every submit.
- **Keeping them in memory**: lost on a restart in the middle of a review.
