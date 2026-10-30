# 0044 — Suggestions from similar earlier bookings

## Context
A bank line got "wie zuletzt" (ADR 0020) only from an earlier bank line
with the same IBAN or exactly the same counterparty name. Card payments
("BILLA DANKT 1234 WIEN", then "BILLA DANKT 5678 GRAZ") and spellings that
differ ("A1 TELEKOM AUSTRIA", "A1 Telekom Austria AG") were missed. Belege
learned nothing from earlier bookings at all; the bulk upload used the
category keywords only, the single receipt form not even those. The user
asked for similar bookings to get the same category suggested during
import.

## Decision
- **One more step in the order**, after "wie zuletzt" for the same
  counterparty and before the category keywords and the model: the newest
  earlier booking that names a similar party.
- **What counts as an earlier booking** (`PastBookings`): effective, with a
  category, not an invoice payment, the newest 1,500. It is recognised by
  its contact, its bank counterparty and its description.
- **Similar** (`match.SimilarParty`): names are folded and legal forms
  dropped as for contacts; numbers (card numbers, dates, terminals),
  payment words ("dankt", "Kartenzahlung") and branch towns are left out.
  Two thirds of the shorter name's words must appear in the longer one; a
  name of one or two words must match in full, so "Magenta Telekom" is not
  "A1 Telekom".
- **What it fills**: category, contact, VAT rate and business share. A
  bank line keeps its own text as the description, since it may name what
  differs; a VAT rate printed on a Beleg still wins.
- **Where**: the bank drafts, the booking form from a bank line, the bulk
  upload of Belege and the single receipt form. Each suggestion says where
  it came from ("ähnliche Buchung", with booking number, date and text)
  and is preselected like the other rule-based ones. Nothing books itself.

## Consequences
- The more is booked, the better the suggestions; a new installation
  learns from its first bookings.
- Within one bulk upload, Belege learn from bookings only, not from each
  other while they wait.
- A wrong suggestion comes from a wrong or ambiguous earlier booking; it is
  visible by its reason and corrected before booking.
- The list of branch towns and payment words is a heuristic for Austrian
  bank exports and may need words added.

## Alternatives considered
- **Fuzzy match on the whole bank purpose:** too much noise (reference
  numbers, dates), and too many false hits.
- **A learned model (e.g. the local LLM, ADR 0020):** stays the last step;
  rules first, explainable and offline.
- **Learn per keyword automatically into the categories:** would change the
  user's category settings behind their back.
