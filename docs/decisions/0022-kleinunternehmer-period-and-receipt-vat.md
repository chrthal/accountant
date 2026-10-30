# ADR 0022: The Kleinunternehmer exemption as a dated period; receipt VAT recorded, not deducted

**Date:** 2026-10-15
**Status:** Accepted. Supersedes ADR 0021's "under the Kleinunternehmer-
regelung every rate is 0 %" for expenses.

## Context
Two findings from the user while testing:
1. *"It would be nice to track the original USt, but in the dashboard (with
   the Kleinunternehmerregelung active) the amount is not calculated correctly,
   since I have no Guthaben."* The overview's USt-Saldo subtracted the VAT on
   expenses and showed a Gutschrift. A Kleinunternehmer deducts no Vorsteuer
   (§ 6 Abs 1 Z 27 UStG), so there is none.
2. *"Make sure that disabling the Kleinunternehmerregelung is counted
   correctly."* The exemption was one switch for all time. Switching it off
   would have made every earlier, exempt expense deductible after the fact.
   The exemption also ends mid-year when revenue exceeds the tolerance band:
   it lapses at the booking that crossed the line (ADR 0010).

## Decision

### Expenses keep the VAT rate of their receipt
Under the exemption an expense is booked with the rate printed on the receipt
(category default or standard rate, ADR 0021). The VAT is recorded, which
documents what was paid and stays correct if the exemption ends. It is
**never deducted**: for anything that computes tax it is part of the cost.
Income under the exemption stays at 0 %.

### The exemption is a period, and the booking date decides
Settings: `kleinunternehmer` (on now), `kleinunternehmer_from` (optional,
empty = from the start) and `kleinunternehmer_until` (the last exempt day,
kept once the switch is off). `Settings.KUOn(date)` says whether a booking
falls into the period, and `KUCoverage(from, to)` gives full, partial or none
for a year or VAT period.

**Switching the exemption off requires the end date.** The save is refused
otherwise, with a message saying why. The settings page explains which date
to enter: the crossing booking after an overshoot of the tolerance band (the
Auswertung names it), or 31.12.

### Where it applies
- **Totals:** `OutgoingVATBlockedCents` (VAT paid in the exempt period),
  `PayableCents()` (owed VAT minus deductible Vorsteuer only), and
  `OutgoingCostCents` (net where deductible, gross where not).
- **Overview:** a fully exempt year shows "keine Zahllast, kein
  Vorsteuerabzug" instead of a saldo. A mixed year shows the saldo of the
  non-exempt part and both VAT figures.
- **VAT page:** Vorsteuer counts only the deductible part, and a column
  "USt n. abz." appears when needed. Fully exempt periods show no Zahllast,
  and partial years name the period.
- **Category breakdown:** the saldo uses the cost (exempt expenses gross).
- **CSV export:** a "Vorsteuer abziehbar" column per expense, and exempt-period
  expenses summarised separately as "Ausgaben, Kleinunternehmer (USt nicht
  abziehbar)".
- **Booking page:** labels the VAT by the booking's own date.
- DATEV writes gross amounts with an empty VAT key (ADR 0010). How exempt-period
  expenses map to accounts is the Steuerberater's account framework.

## Found while testing
The new date fields were validated on save but never written. A switch-off
was therefore accepted and the end date lost, which is exactly the
retroactive deduction this ADR exists to prevent. The HTTP test of the real
flow caught it: switch off, book in April, look at the overview. The settings
round-trip test now reads the period back.

## Consequences
- One period only. Re-entering the regime after leaving it (with its own
  waiting rules) is not modelled and is a question for the Steuerberater.
- Invoices still snapshot the switch at issue time (ADR 0008), so an invoice
  issued in the exempt period keeps its exemption note.

## Alternatives considered
- **Force 0 % on all exempt-period expenses (ADR 0021's first take):** this
  loses the receipt's VAT, which the user wants recorded, and would need
  rebooking if the exemption is backdated.
- **A per-booking "deductible" flag:** it duplicates what the date already
  says, and can drift from the period when the period is corrected.
- **A list of periods:** more general, but no real case needs it yet.
