# Changelog

What changed in each release, for the people who use the app. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions
follow [Semantic Versioning](https://semver.org/). Decisions behind the changes
are in [`docs/decisions/`](docs/decisions/README.md).

A release tag `vX.Y.Z` needs a `## [X.Y.Z]` section here: the release workflow
takes its notes from it and stops if it is missing.

## [Unreleased]

### Changed

- **Published on GitHub** under the MIT license. CI and releases run on
  GitHub Actions; the image is `ghcr.io/chrthal/accountant`.

## [0.4.0] - 2026-10-18

### Added

- **Change the business share of a booking** on its page. The profit and
  Vorsteuer of the booking's period follow; the share it was booked with
  and every change stay visible, and the page says to check a VAT return
  already filed. Not for income, reversed bookings or assets (ADR 0045).
- **Business share in the bulk lists**: the review list of a bulk Beleg
  upload and the bank drafts have a field for the business share and how
  it was found, so a phone bill can be booked partly private there too
  (ADR 0041).
- **Suggestions from similar earlier bookings**: a bank line or Beleg whose
  counterparty or vendor resembles an earlier booking (similar name, card
  numbers and branch towns ignored) gets its category, contact, VAT rate
  and business share, marked "ähnliche Buchung" with the booking it came
  from. Receipts, single and bulk, now learn from earlier bookings too, and
  the single receipt form also uses the category keywords (ADR 0044).
- **Change a booking's description** on the booking page. Amounts, date and
  assignment stay as booked; the original description and every change are
  kept and shown, and the new one is used in lists, search and exports
  (ADR 0043).

### Changed

- **Bulk upload of Belege is faster**: the upload only stores the files and
  returns at once; they are then read in the background, up to four at a
  time. The waiting list shows which are still being read and reloads by
  itself, but never over what you typed (ADR 0042).

### Fixed

- A new installation offers the restore from a backup again after its first
  automatic backup; that backup's time no longer counts as data (ADR 0030).

## [0.3.0] - 2026-10-17

### Added

- **Email reminders** to your own address via your mail server: recurring
  invoices due, overdue invoices, old or failed backups, waiting receipts.
  Each once, several in one mail; never to customers. The mail server is
  set up in the settings, with a test mail; the password is never shown
  again and stays out of backups. Environment variables still work and win
  (ADR 0039, ADR 0040).
- **Private transfers**: a bank line can be marked private (Privatentnahme
  or Privateinlage). It counts in no total and leaves the open list.
  Transfers to or from the owner's own name are recognised and can be
  marked all at once; the year's sums are on the report page. Own transfers
  already booked as income or expense are moved to private in one click:
  Storno on the same day, then marked private (ADR 0035).
- **Recurring invoices** (e.g. hosting): a template per customer with a
  rhythm; a week ahead the overview and invoice list remind, "Rechnung
  erstellen" opens the filled form, issuing moves the next date on. Listed on
  the customer; nothing is sent automatically (ADR 0038).
- **Business share for mixed-use expenses** such as phone and internet: a
  booking can be partly private (default 100 % business). Only the business
  share counts for the profit and as Vorsteuer, none under 10 % business
  use; the full amount stays booked. Shown on the booking, in the list, the
  report, the VAT overview and the exports; next month's bill from the same
  counterparty takes over the share (ADR 0041).
- **Bewirtung 50 %**: a category can be deductible by half; it changes the
  profit, not the VAT. New installations get a Bewirtung category (ADR 0037).
- **Depreciation (AfA)**: an expense can be written off as an asset over its
  useful life (linear, half-year rule), with an Anlagenverzeichnis page, a
  Gewinn block on Jahr & Export, and both in the CSV export (ADR 0037).
- **Partial payments** on invoices: from the invoice page with an amount, or
  from a bank line that names the invoice and pays less. Split over the VAT
  rates; the last payment books the exact rest (ADR 0036).

## [0.2.0] - 2026-10-16

Moving over from another tool, receipts in bulk, and books that stay
readable and restorable without a command line.

### Added

- **Import invoices written with another tool** under Rechnungen: upload the
  PDF, check the pre-filled number, date, recipient and amount, save. The
  original PDF stays the document, and new numbers continue after imported
  ones (ADR 0031).
- **Bulk import of invoices**: choose many PDFs at once, check them in one
  list, import the ticked ones. Rows with a missing or duplicate number are
  not ticked and say why (ADR 0032).
- **Bulk upload of receipts** under Beleg erfassen: many photos and PDFs at
  once, checked in one list with a preview, each booked with its receipt.
  Category and VAT rate are suggested; a receipt booked already is named
  (ADR 0033).
- **Year archive** under Auswertung: one ZIP with the bookings CSV, every
  receipt and every issued invoice as PDF, readable without the app
  (ADR 0029).
- **Restore a backup in the browser**: a new installation offers an upload
  under Einstellungen → Datensicherung. The file is checked completely
  before anything changes; existing books are never overwritten (ADR 0030).
- **Exports for the tax adviser in the settings**, with a choice of year:
  the seven years of the retention period (§ 132 BAO) and any other year
  with bookings, invoices or bank transactions.
- **Default categories**: a new installation starts with 23 generic
  categories for a small Austrian business of any trade, with keywords for
  nationwide payees (Magenta, SVS, WKO, OMV, ÖBB …) and a default VAT rate
  where it is always the same (ADR 0027).
- **DATEV can be switched off** in the settings. Off, the Konto/Gegenkonto
  fields and the export are hidden; values already entered are kept
  (ADR 0028).
- **Help page** (Hilfe, in the navigation) explaining every part of the
  app, in German and English.

### Changed

- **DATEV is off unless it was in use.** An installation without a DATEV
  consultant number now hides the DATEV fields and export. Switch it on
  under Einstellungen → DATEV-Export if you need it.
- The bookings CSV has a new last column, "Belege", naming each booking's
  receipt files.

### Upgrading

- Three database migrations (0014–0016) run automatically on the first
  start. Existing books are not changed; default categories are only added
  to an empty installation.

## [0.1.0] - 2026-10-16

The first usable release: bookkeeping, invoices, bank import, reports and
backups for a small Austrian business.

### Added

- **Bookings**: income and expenses with VAT per booking, contacts and
  categories. The ledger is append-only; a wrong booking is reversed by a
  Storno (ADR 0007).
- **Receipts (Belege)**: photos and PDFs stored with the booking in the
  database. Receipt photos are read by Tesseract OCR, supplier PDF invoices by
  poppler, and the form is pre-filled. Both are optional (ADR 0011, 0016,
  0018).
- **Invoices** with a VAT rate per line, the § 11 UStG mandatory fields, and
  the Kleinunternehmer exemption note (ADR 0008).
  - PDF layout with logo, a details block, a payment block with an
    EPC/Stuzza payment QR code, and a footer. Invoices over several pages
    repeat the table header, carry an "Übertrag" and number their pages
    (ADR 0025).
  - Numbering per year (`2026-0001`) or per day (`E260112001`), with an
    optional prefix, so a number need not show how many invoices were written
    in the year (ADR 0025).
  - Drafts: save the form as typed, preview a watermarked PDF without a QR
    code, issue later. A draft has no number until it is issued (ADR 0026).
  - Payments are booked from the invoice or from a bank line; open and
    overdue amounts are shown (ADR 0015).
- **Bank import** from CSV with column mapping, including meinELBA exports
  with labelled text (ADR 0009, 0014).
  - Matching to bookings and invoices, by invoice number, amount and name.
  - Booking drafts for open bank lines: "wie zuletzt" from an earlier booking,
    category keywords, and an optional local LLM via Ollama for first-time
    payees. Nothing books itself (ADR 0020, 0021).
- **Kleinunternehmerregelung** as a dated period. Expenses in the exempt
  period keep their receipt VAT, which is recorded and never deducted
  (ADR 0022). Tracker for the €55,000 threshold and the €60,500 tolerance.
- **SVS** Kleinunternehmer limits next to the VAT threshold (ADR 0023).
- **Reports**: overview, VAT per month and quarter, totals per category,
  CSV export and DATEV EXTF export (ADR 0003, 0010, 0012).
- **Backups**: verified logical backups as a download and daily into a
  directory; restore only into an empty database, also across SQLite and
  Postgres (ADR 0019).
- **Settings** for language (Deutsch, English) and date format. Date fields
  follow the date format and keep a calendar picker (ADR 0024).
- **Design**: mobile-first, light and dark theme, installable as a PWA
  (ADR 0004, 0013).
- **Security**: optional login via `BOOKKEEPER_PASSWORD`, refusal of
  cross-origin POSTs, a strict Content Security Policy (ADR 0017).
- **Operations**: one binary and an amd64 OCI image with OCR and PDF tools;
  PostgreSQL or SQLite through `BOOKKEEPER_DSN`; a podman/docker dev setup
  (ADR 0005, 0006).

### Notes

- Not tax advice. Edge cases are flagged in the app rather than guessed at;
  check the numbering scheme and the DATEV account mapping with your
  Steuerberater.

## [0.0.1] - 2026-08-18

### Added

- Project scaffold: build, container image and release workflow.

[Unreleased]: https://github.com/chrthal/accountant/compare/v0.4.0...main
