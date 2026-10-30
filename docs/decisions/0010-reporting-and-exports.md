# ADR 0010: Reporting and exports — threshold tracker, CSV, DATEV EXTF

**Date:** 2026-08-18
**Status:** Accepted

## Context
Phase 4 adds the three outputs the MVP plan asks for: a Kleinunternehmer
threshold tracker, a plain CSV export, and a DATEV EXTF Buchungsstapel (ADR
0003). Two of the three touch tax rules, which is exactly where CLAUDE.md says
to flag uncertainty rather than ship something plausible.

## Decision

### The threshold tracker computes, it does not decide

§ 6 Abs 1 Z 27 UStG: the Kleinunternehmer limit is **€55.000 gross per year**,
with a 10 % tolerance band to **€60.500**. Exceeding by ≤10 % keeps the exemption
through year-end and drops it the following year; exceeding by >10 % ends the
exemption immediately, retroactive to the transaction that crossed the line.

The tracker reports three things and nothing more: running gross revenue for the
year, which of the three bands it falls in, and — when the tolerance ceiling is
passed — *which booking crossed it*, since that is the point the retroactive
consequence attaches to.

What it deliberately does **not** do is decide tax consequences. It does not
model which revenues count towards the limit (some are excluded), the rule about
re-entering the scheme after leaving it, or any cross-border case. Each of those
changes the answer and none can be inferred from the data the app holds. The UI
says so, next to the number, rather than in a document nobody reads.

Revenue is measured over **effective entries** — a booking that was reversed, and
its Storno, both drop out (ADR 0007). A correction must not inflate the figure
the exemption depends on.

### CSV export is for humans and spreadsheets

Semicolon-separated with a UTF-8 BOM, because that is what opens correctly in a
German-locale Excel without an import dialogue. Amounts in Austrian notation,
dates ISO. One row per booking with every field including Konto/Gegenkonto, plus
a VAT summary grouped by rate. This is the fallback ADR 0002 chose and ADR 0003
kept: readable, obvious, no format to get wrong.

### DATEV EXTF is written to spec but is NOT validated

Generated as ADR 0003 requires: the `EXTF` header line, the column-header row,
then one row per booking, semicolon-separated, quoted text fields, amounts with
a decimal comma, dates as `DDMM`, and **cp1252 encoding** — DATEV does not read
UTF-8.

Three values the format needs cannot be invented: **Berater-Nummer**,
**Mandanten-Nummer** and **Sachkontenlänge**. They live in settings, start empty,
and the export refuses to run until they are filled in, with a note that they
come from the Steuerberater. Emitting an EXTF file with placeholder numbers would
produce something that imports into the wrong client's books.

**ADR 0003 states the criterion for this being done: the file passes DATEV's own
DatevFormatPrüfProgramm.** That program has not been run — it is Windows software
and there is no DATEV installation here. So this exporter is written against the
documented structure and tested for internal consistency, and it is explicitly
**unvalidated**. The plan records Phase 4 as incomplete for that reason, and the
export page says the same to the user rather than implying otherwise.

Soll/Haben: an income booking is `S` on the Konto, an outgoing `H`, with
Gegenkonto as written on the entry. Whether that orientation matches the
Steuerberater's account framework is part of what the Prüfprogramm run — and a
conversation with them — has to confirm.

## Consequences
+ The threshold number is visible continuously, which is the point: crossing it
  unnoticed is the failure mode that costs money.
+ The CSV export works today with no agreement needed from anyone.
+ The DATEV exporter exists and can be validated in one sitting once someone with
  a DATEV installation runs the Prüfprogramm on a real file.
- Phase 4 cannot be marked done. That is the honest state, not a gap to paper
  over.
- The tracker will look wrong to anyone whose revenue includes items outside the
  limit's scope. Hence the caveat sitting next to the figure.
- cp1252 means a Buchungstext with characters outside it degrades. Booking texts
  are typed by the user, so this is visible rather than silent.

## Alternatives considered
- **Shipping DATEV without saying it is unvalidated:** rejected. A wrong
  Buchungsstapel is worse than none, because it looks importable.
- **Guessing Berater-/Mandanten-Nummer or defaulting them to zeros:** rejected
  for the same reason — a file that imports into the wrong books.
- **Making the tracker judge whether the exemption still applies:** rejected.
  That is a determination for the Steuerberater; the app supplies the arithmetic.
