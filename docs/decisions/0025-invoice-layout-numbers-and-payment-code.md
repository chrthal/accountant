# 0025 — Invoice layout, numbers per day, logo and payment QR code

## Context
Three things were asked for: the invoice number must not tell a customer how many invoices were
written this year, a logo, and a QR code a customer scans to pay.

ADR 0008 numbers invoices `YYYY-NNNN`, counted through the year. `2026-0042`
says "42nd invoice this year".

## Decision
- **Two numbering schemes**, chosen in the settings; new invoices use the
  chosen one, issued invoices keep their number:
  - `year`: prefix + `YYYY-NNNN` (ADR 0008, the default).
  - `day`: prefix + `YYMMDD` + `NNN`, counted per day: `E260112001` is the
    first invoice of 12 January 2026. Up to 999 per day.
  Both share `UNIQUE (year, seq)`: a day number stores `MMDD*1000 + n` as
  `seq` (always ≥ 101001), a year number stays below 100000, so switching
  schemes mid-year cannot collide. `number` stays `UNIQUE` as before. The
  prefix is up to 8 letters, digits or dashes.
- **Payment QR code**: the EPC QR code (EPC069-12, version 002, error
  correction M), which Austrian banking apps read as the Stuzza code. It
  carries name, IBAN, optional BIC, the gross amount and "Rechnung <number>"
  as the remittance text, so the payment comes back with the number and
  bank matching (ADR 0020) finds the invoice. It is printed only when an
  IBAN is set. Encoder: `rsc.io/qr` (BSD, no dependencies); the PDF draws
  the modules as vector squares.
- **Logo**: one, in a `logo` table (PNG or JPEG, ≤ 1 MiB, ≤ 4000 px). A PNG
  is decoded and written again as 8-bit RGBA, because fpdf cannot draw
  interlaced or 16-bit PNGs; a JPEG is kept as uploaded. Every upload is
  checked by drawing it, so a file fpdf rejects never reaches an invoice.
  The logo is in backups.
- **Layout** after the example: logo and "Rechnung" on top, recipient left,
  issuer and details right, columns Bezeichnung / Menge / Preis p. E. /
  Zwischensumme / USt. % / USt. / Gesamt, the exemption note beside the
  totals, the user's note left of the payment block, a three-column footer.
  BIC and a free "contact for the footer" text are new issuer settings and
  are snapshotted onto the invoice like the other issuer details (ADR 0008).
- **Several pages**: rows never split; a row that does not fit starts a new
  page with the table header again and an "Übertrag" line (the sums so
  far). Every page after the first names the invoice and its date. With
  more than one page, every page says "Seite n von m": the PDF is drawn
  twice, once to count the pages. The footer is the issuer's name and
  address, the contact text, and the account, under a hairline.
- **Embedded font**: DejaVu Sans Condensed (regular, bold) from the fpdf
  module, embedded and subset. With the non-embedded Helvetica, macOS
  drew "€" wider than fpdf's metrics and the next word ran into it. The
  embedded font also prints names outside cp1252 instead of "?".

## Consequences
- A logo change shows on old invoices when their PDF is opened again. The
  logo is design, not a § 11 field; the content of an issued invoice does
  not change.
- PDFs grow from about 3 KB to about 55 KB (font subset, logo).
- About 1.3 MB of font files are in the repository and the binary.
- **Not tax advice:** § 11 UStG asks for a unique number from one or more
  number ranges. A range per day is in common use, but the user should
  confirm it with the Steuerberater. The settings page says so.
- The QR code always asks for the full gross amount, also when part of the
  invoice is already paid.

## Alternatives considered
- **Random invoice numbers**: reveal nothing, but are hard to reconcile with
  "fortlaufend". Rejected.
- **Year scheme with a random start**: still reveals the count from two
  invoices. Rejected.
- **An own QR encoder**: about 500 lines of Reed-Solomon and masking code
  to maintain. `rsc.io/qr` is small and stable.
- **Logo as a file on disk**: the container is stateless (ADR 0005); the
  database is the one place that is backed up.
