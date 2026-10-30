# 0027 — Default categories for a new installation

## Context
A new installation had no categories, so the first bank import sorted
nothing, and the user had to know what a small business usually needs. The
user asked for a default set, for a new, empty instance only, and kept
generic: the app is for small businesses of any trade, not for one user's
bank account.

## Decision
- `store.DefaultCategories` holds 23 categories: three for income, 20 for
  expenses (Wareneinkauf & Material, Fremdleistungen, Büromaterial,
  Software & IT-Dienste, Hardware & GWG, Telefon & Internet, Strom, Gas &
  Energie, Miete & Betriebskosten, Instandhaltung & Reparatur, Kfz-Kosten
  (PKW), Reisekosten, Werbung & Marketing, Fortbildung & Fachliteratur,
  Porto & Versand, Steuer- & Rechtsberatung, Versicherungen, SVS, Beiträge &
  Gebühren, Bankspesen, Sonstige Ausgaben). None is specific to one trade.
- **Seeded only into an empty database**: when the server starts and no
  table that backups cover has a row. A database with anything in it,
  including one whose categories were deleted on purpose, is not touched.
- **Not a migration**: a restore migrates its target first and requires it
  empty (ADR 0019). Seeding at server start keeps `bookkeeper restore` into a
  new database working.
- **Keywords** (ADR 0021) name only nationwide Austrian payees whose name
  means that payee and nothing else: telecoms, energy providers, insurers,
  SVS, WKO, fuel stations, ÖBB, Post and parcel services, office and
  electronics chains. Words with a second meaning are left out ("drei",
  "merkur", "wifi", "hot"), and so are payees that sell anything (Amazon,
  PayPal). Trade-specific suppliers are for each user to add. Tests check
  that every keyword hits its own category and that ordinary texts hit none.
- **Default VAT rate** only where it is the same on every receipt:
  20 % for office supplies, hardware, telephone, tax adviser; 0 % for
  insurance, SVS, chamber fees, bank charges and interest. Where it varies
  (travel at 10 % or 13 %, rent, foreign software, hosting and advertising
  under reverse charge, energy for a home office) it is left open or set to
  the usual rate, and the user changes it per booking.
- **Kfz-Kosten (PKW)** default to 0 %: the Vorsteuer on a passenger car is
  not deductible in Austria (§ 12 Abs 2 Z 2 lit b UStG), so the receipt is
  booked gross. Electric cars and trucks differ.
- No DATEV accounts: the chart of accounts is for the Steuerberater
  (ADR 0003).
- Checked for false hits against a real meinELBA export: 19 of 75 lines
  assigned, none wrong.

## Consequences
- **Not tax advice.** Bewirtung (50 % rule) and private withdrawals are not
  in the set: the app cannot book either correctly yet, and a category
  would suggest it can. (Later: private transfers are bank lines marked
  private, ADR 0035; Bewirtung at 50 % joined the set with ADR 0037.)
- Existing installations get nothing. Categories are added in the UI there.
- A restore into a database the server has already started on fails as
  "not empty", as before for any data: restore into a new database.

## Alternatives considered
- **A button that adds the set to existing data**: declined by the user.
- **A seed migration**: breaks restore, see above.
