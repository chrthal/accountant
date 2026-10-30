# 0035 — Private transfers: bank lines outside the books

## Context
In an Einnahmen-Ausgaben-Rechnung, money the owner takes out of the business
(Privatentnahme) or puts in (Privateinlage) is neither income nor expense.
The bank import had no place for such lines: they stayed open forever, or
were booked as expenses, which made the profit wrong. In a typical sole
trader's export, a good share of the lines are transfers to the owner.

## Decision
- A bank line can be **marked private** (`bank_transactions.private_at`).
  A private line is not matched to a booking, counts in no total, and is
  left out of the open list, the overview's to-do count and the booking
  suggestions. "Privat aufheben" undoes it.
- Private and matched exclude each other: a matched line cannot be marked
  private, and a private line cannot be matched.
- **Recognition**: a line whose counterparty is the issuer's own name, in
  any word order (`match.SimilarName`), is flagged as an own transfer. The
  bank page highlights "Privat" for it and offers "Alle als privat
  markieren" for all of them; the booking suggestions skip them.
- The kind follows the sign: money out is a Privatentnahme, money in a
  Privateinlage. **Jahr & Export** shows both sums for the year.
- The flag is in backups.
- **Converting booked transfers**: own transfers that were already booked as
  income or expense (before this ADR, or by mistake) are offered "Als privat
  umbuchen", per line and for all at once. In one transaction it reverses
  each matched booking by a Storno **on the booking's own date**, so every
  year's figures come out right, removes the match and marks the line
  private. Invoice payments and assets are refused and not offered: they
  belong to the books, and undoing them is a decision of its own. The
  original booking and its Storno stay, as for any correction (ADR 0007).

## Consequences
- The profit, the VAT and the Kleinunternehmer threshold no longer see
  these transfers.
- Private lines are not in the bookings CSV, which lists bookings only. Their
  sums are on the report page.
- A GmbH paying its managing director a salary is not a private transfer;
  the recognition only suggests, and the user decides.

## Alternatives considered
- **Bookings of a third kind "privat"**: every total, export and report
  would have to filter them out, and they would look like part of the books.
- **Ignoring lines without a flag**: nothing would say why a line is not
  open, and it could not be undone deliberately.
