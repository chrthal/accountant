# 0036 — Partial payments on invoices

## Context
ADR 0015 made invoice payments all or nothing: a payment always booked the
whole invoice, and a transfer that paid less could not be booked against
it. Customers pay in instalments, short-pay by a fee, or pay a deposit. The
user asked for partial payments.

## Decision
- A payment has an **amount**, up to what is still open. The invoice form
  pre-fills the open amount; less is a part payment, more is refused.
- **Split over the VAT rates** in proportion to what each rate still has
  open: shares by largest remainder, each share's VAT from that rate's open
  VAT, rounded half up. The payment that settles the invoice books **each
  rate's exact rest**, so a fully paid invoice shows in the ledger exactly
  the net and VAT it charged, as ADR 0015 required.
- Booking texts say "Teilzahlung", "Restzahlung" or "Zahlung".
- **Status** "teilweise bezahlt" in lists and on the overview; open items
  count what is still open.
- **Bank**: a transfer that names the invoice number and pays less than is
  open is offered as "Teilzahlung Rechnung …". It is never part of "Alle
  eindeutigen zuordnen", which still needs number and exact amount. A
  partly paid invoice stays a candidate, so the rest matches by amount.
- A Storno of a payment reopens exactly what it booked.

## Consequences
- Supersedes the all-or-nothing rule of ADR 0015; its exact-VAT rule holds
  for the settled invoice.
- Between payments, the VAT booked so far is a proportion of the invoice's
  VAT. Under cash-basis accounting that is the VAT of the money received.
- **Not tax advice**: whether a short payment (Skonto, a bank fee) reduces
  the VAT owed is not modelled; the rest stays open and is the user's
  decision.

## Alternatives considered
- **A part payment booked at one rate only**: wrong VAT on mixed-rate
  invoices.
- **Writing off short payments automatically**: a guess about money that may
  still come.
