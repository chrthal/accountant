# 0026 — Invoice drafts

## Context
An issued invoice is immutable (ADR 0008): the database refuses changes, and
a wrong invoice is corrected by a new one, as § 11 UStG expects. The user
asked for an invoice editor. Up to now the form either issued an invoice or
was lost, so there was no way to prepare one, look at it, and send it later.

## Decision
- **A draft is the form as typed**, stored as JSON text in
  `invoice_drafts` (`data`, `created_at`, `updated_at`). Nothing is checked
  on save: a missing recipient or a half-typed price is kept as it is.
- **No number until it is issued.** Drafts can be changed and deleted at any
  time and never use up a number. Issuing goes through the same
  `CreateInvoice` as before; the draft is deleted in the same transaction,
  so a draft cannot be issued twice.
- **No foreign key to contacts**, so a draft never blocks deleting a
  contact. A draft whose contact is gone asks for a recipient again.
- **Preview** builds the document with the current settings and the number
  `ENTWURF`, without writing anything. The PDF says "Rechnungsentwurf" and
  carries a watermark on every page. It has no payment QR code, only a
  placeholder, so a preview cannot be paid by mistake.
- The form has three buttons. "Als Entwurf speichern" comes first, because
  Enter presses the first submit button, and a draft can be undone.
  "Rechnung ausstellen" asks for confirmation.
- Drafts are listed above the invoices with recipient, date, number of
  positions and the total of the lines that parse.
- Drafts are in backups.

## Consequences
- The issued invoice is still what `CreateInvoice` validates. A draft only
  postpones that check.
- A preview uses today's settings. If the settings change before issuing,
  the issued invoice follows the new ones.
- Two tabs on one draft: the last save wins. Saving a draft that has been
  issued or deleted in the meantime shows a message and keeps the input.

## Alternatives considered
- **Editable issued invoices**: against § 11 UStG and ADR 0008.
- **Drafts in the invoice tables with a status**: would weaken the
  append-only triggers and the gapless numbering, for no gain.
- **Drafts as typed columns and a lines table**: half-finished input would
  not fit, and the form already holds everything as text.
