# 0028 — DATEV can be switched off

## Context
The DATEV export (ADR 0003, ADR 0010) brings Konto/Gegenkonto fields on
bookings and categories, three settings, and warnings on the report page.
Many Austrian tax advisers work with BMD or RZL, and many small businesses
only hand over the CSV file. For them all of this is noise. The user asked
for a switch.

## Decision
- A setting `datev_enabled`. Off, the app hides the DATEV settings, the
  Konto/Gegenkonto fields and columns, the DATEV export button and its
  warnings, and refuses `/export/datev` with 404.
- **Nothing is deleted.** Accounts on categories and bookings, and the DATEV
  numbers, are kept. Forms carry them along in hidden fields, so saving a
  category or the settings while DATEV is off does not erase them.
  Switching DATEV on again shows everything as it was.
- **Default**: off for new installations. An installation from before the
  switch keeps DATEV on if its DATEV consultant number is set, because then
  it was in use.
- The CSV export is unchanged and still lists Konto/Gegenkonto where set.

## Consequences
- With DATEV off, new bookings still take the category's default accounts
  if the category has any, so switching on later finds them filled.
- The template function `datev` reads the setting from the cached
  preferences, like language and date format (ADR 0024).

## Alternatives considered
- **Removing the fields when off**: switching back would lose data.
- **Hiding DATEV automatically while no numbers are set**: the account
  fields would appear and disappear without the user deciding it.
