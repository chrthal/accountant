# ADR 0015: Invoice due dates and payments

**Date:** 2026-09-29
**Status:** Accepted

## Context
Plan 0001's Phase 2 noted that invoices never reach the ledger. Issuing
an invoice books nothing, and the app cannot say which invoices are paid. On a
cash basis (Einnahmen-Ausgaben-Rechnung, § 4 Abs 3 EStG) income is recognised
when the money arrives, not when the invoice is written. So the missing piece
is a payment step. A link at issue time would be the wrong model.

Invoices are immutable in the database (ADR 0008), so "mark as paid" cannot
be a column update on the invoice.

## Decision

### A payment is an ordinary income entry pointing at the invoice
`entries.invoice_id` (nullable) links a booking to the invoice it pays. "Paid"
is derived: the sum of *effective* entries (neither a Storno nor reversed)
with that `invoice_id`, compared with the invoice gross. It is computed in
`invoiceSelect`, so every invoice read carries `PaidCents` and `LastPaidOn`.

An invoice's status (bezahlt / überfällig / offen) is therefore never stored.
It follows from the ledger, and the ledger's rules apply unchanged: a wrong
payment is undone by a Storno, after which the invoice is open again.

### One entry per VAT rate, with the invoice's own figures
A mixed-rate invoice (20 % + 10 %) is paid as two income entries, one per VAT
group. Each carries the group's net and VAT **exactly as the invoice computed
them** (per line, then summed). It is not a fresh `SplitVAT` of the gross,
which can differ by a cent. This is what makes the booked VAT equal the
invoiced VAT, and the test checks it to the cent.

A group with a zero net (free positions) books nothing, since the ledger
refuses zero amounts.

### All or nothing
An invoice with any effective payment refuses another one (`ErrAlreadyPaid`,
409). **Partial payments are not supported.** They change what "offen" means
in every list and report, and nothing real has asked for them yet. That is
flagged in plan 0002.

### Due date and IBAN are issue-time facts
Two new invoice columns, `due_date` and `issuer_iban`, are written once at
issue time and snapshotted like the rest of the issuer (ADR 0008). The
default due date is issue date + `payment_terms_days` from settings, and the
user can change it. Invoices issued before this migration have neither, and
are never overdue. The PDF prints "Zahlbar bis" and, when an IBAN is
configured, a payment note.

### The Storno does not carry the link
`ReverseEntry` does not copy `invoice_id`. The Storno of a payment is a
correction, not a negative payment, and keeping the link off means "paid" is a
plain sum over effective payment entries.

## Consequences
- The invoice page has a "Zahlung buchen" action (date of receipt, optional
  income category for account defaults). The ledger shows payments with a
  link back to their invoice.
- Migration 0006 is an `ADD COLUMN` on both engines. It rewrites no rows and
  fires no triggers, so the immutability triggers are unaffected.
- Reconciling the payment against the bank (plan 0002, Phase 9) needs a
  transaction to match *several* entries, one per rate. See ADR 0014.

## Alternatives considered
- **A `payments` table separate from entries:** that is a second place where
  money is recorded, and the income would still have to be booked to count.
  Two records of one event is how books drift apart.
- **Book the income at issue time (accrual):** wrong for
  Einnahmen-Ausgaben-Rechnung, and it would put unpaid invoices into the
  Kleinunternehmer revenue figure early.
- **One payment entry at the invoice's dominant rate:** simpler, and wrong
  for every mixed-rate invoice.
