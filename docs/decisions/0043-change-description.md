# 0043 — A booking's description can be changed

## Context
Bookings are append-only, enforced by database triggers (ADR 0007). A typo
or a vague bank text ("Rechnung 4711") in the description could only be
fixed with a Storno and a new booking. That is heavy for text that changes
no figure, and it breaks the bank match and the Beleg link of the booking.
The user asked to change the title after creation.

Austrian law does not forbid changes, only hiding them: an entry must not
be changed in a way that makes the original content unknowable (§ 131 Abs 1
Z 8 BAO).

## Decision
- **Only the description.** Amounts, VAT, date, direction, category,
  contact and the account tags stay as booked; a mistake there is still a
  Storno.
- **The booking row is not touched.** A change is a new row in
  `entry_descriptions` (entry, text, time), append-only with its own
  triggers like `entries`. The newest row is the description shown
  (`currentDescription`), in the booking query, the search and the
  Kleinunternehmer revenue list; everything built on them (lists, exports,
  "wie zuletzt", the Storno text) follows.
- **The booking page** has "Beschreibung ändern" and lists the history:
  "gebucht als" with the booking time, then each change with its time.
- The new table is in the backups.

## Consequences
- The ledger keeps every version; nothing is lost, and the database still
  refuses UPDATE and DELETE on bookings.
- A Storno takes the description current at that moment.
- Exports show the current description only; the history is in the app
  and in the backup. To confirm with the Steuerberater whether the
  original should also appear in the export.

## Alternatives considered
- **Lift the trigger for the description column:** simpler, but the
  original would be lost, which § 131 BAO does not allow.
- **Storno and rebook:** stays the way for figures; for text alone it
  breaks links and adds two bookings.
- **Make category and contact changeable too:** they move figures between
  categories in reports already handed over; not asked for, and a Storno
  covers it.
