# 0033 — Bulk upload of Belege

## Context
"Beleg erfassen" (ADR 0011, 0016) reads one photo or PDF and opens the
booking form. After a month of receipts, or when moving over with a year of
them, that is one form per receipt. The user asked for the bulk import of
invoices (ADR 0032) to work for Belege too.

## Decision
- **Same shape as the invoice bulk import.** Many photos and PDFs at once
  (`POST /receipts/batch`), each read by the same function as a single
  receipt and put on a waiting list, `receipt_queue`. The two waiting lists
  share one store implementation (`store.Queue`) and are both left out of
  backups.
- **A proposal per receipt**: date, gross amount, description (vendor and
  invoice number), outgoing. The category comes from the categories'
  keywords (ADR 0021). The VAT rate is the one printed on the receipt, else
  the category's default, else the standard rate. A contact is chosen when
  the vendor's name fits one.
- **Review in one list with a preview** of photos and a link to PDFs. A row
  is ticked in advance only when it is complete and not booked yet; it
  names why not: no amount, no date, no description, **the receipt is
  booked already** (same SHA-256 as a stored Beleg, with the booking's
  number), or the same file is in the list twice.
- **"Angehakte buchen"** books each ticked row with its Beleg through
  `CreateEntryWithReceipt`: the booking, the Beleg and the removal from the
  waiting list are one transaction. A bulk booking never leaves a booking
  without its receipt, nor a receipt that can be booked twice. Failing rows
  stay with their error; every edit is saved to the list first.
- **"Im Formular öffnen"** opens a row in the ordinary booking form, with
  the receipt taken from the waiting list, for a receipt to match to a bank
  line or to look at the recognised text. Saving there uses the same
  transaction.

## Consequences
- Checked in the real image: a receipt photo was read by OCR (date, amount,
  vendor, the printed 10 %), a PDF invoice by its text layer; both were
  booked with their files, the photo byte for byte.
- OCR on photos takes seconds each; a large batch is one long upload.
- Single receipts still book in two steps (booking, then Beleg), as before.

## Alternatives considered
- **One combined waiting list for invoices and receipts**: the review asks
  for different fields, and the two lead to different records.
