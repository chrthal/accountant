# Architecture Decision Records

Why things are the way they are. One file per decision, `NNNN-title.md`,
sections: Context / Decision / Consequences / Alternatives considered.

Superseded ADRs stay in place — the record of a reversed decision is part of the
history. Mark the old one's status and point at the new one.

| # | Title | Status |
| --- | --- | --- |
| [0001](0001-tech-stack.md) | Initial tech stack (Go, SQLite, html/template + htmx) | Accepted; database choice superseded by 0006, htmx by 0013 |
| [0002](0002-sevdesk-scan-scope.md) | sevdesk feature scan — scope adjustments | Accepted; decision 3 superseded by 0003 |
| [0003](0003-datev-export.md) | DATEV Buchungsstapel export | Accepted — supersedes 0002 §3 |
| [0004](0004-mobile-pwa-not-flutter.md) | Mobile support — responsive web app, not a native app | Accepted |
| [0005](0005-build-and-release.md) | Build & release — containers, GitHub Actions, GitHub Container Registry | Accepted |
| [0006](0006-external-database.md) | PostgreSQL primary, SQLite retained for dev | Accepted — supersedes 0001's database choice |
| [0007](0007-core-schema.md) | Core schema — integer cents, ISO dates, DB-enforced append-only | Accepted; required account tags superseded by 0012 |
| [0008](0008-invoices.md) | Invoices — YYYY-NNNN numbering, snapshotted issuer, fpdf | Accepted; numbering extended and layout replaced by 0025 |
| [0009](0009-bank-import.md) | Bank import — generic mapped CSV, not per-bank parsers | Accepted; matching and mapping extended by 0014 |
| [0010](0010-reporting-and-exports.md) | Reporting & exports — threshold tracker, CSV, DATEV EXTF (unvalidated) | Accepted |
| [0011](0011-receipt-ocr-and-pwa.md) | Receipt OCR via a Tesseract subprocess, and a shell-only PWA | Accepted; extended to PDFs by 0018 |
| [0012](0012-account-tags-optional.md) | Konto/Gegenkonto optional per entry, defaulted per category | Accepted — supersedes 0007's required account tags |
| [0013](0013-design-system-and-no-htmx.md) | Design system, app shell, and one small script instead of htmx | Accepted — supersedes 0001's htmx |
| [0014](0014-bank-matching-many-and-guessed-mappings.md) | Bank matching: one transfer to several bookings; guessed and remembered column mappings | Accepted — extends 0009 |
| [0015](0015-invoice-payments.md) | Invoice due dates and payments as ledger entries | Accepted; all-or-nothing replaced by 0036 |
| [0016](0016-belege-in-the-database.md) | Belege stored in the database, append-only | Accepted |
| [0017](0017-security-baseline.md) | Security baseline: cross-origin protection, CSP, optional single-password login | Accepted |
| [0018](0018-pdf-invoice-import.md) | Supplier PDF invoices: text layer first, OCR for scans | Accepted — extends 0011 |
| [0019](0019-backups.md) | Backups as a verified logical export, restorable into either engine | Accepted |
| [0020](0020-bank-assist-rules-first-llm-optional.md) | Bank-line assistance: rules first, an optional local LLM for the rest | Accepted — extends 0009, 0014 |
| [0021](0021-category-keywords-and-default-vat.md) | Category keywords and default VAT rates | Accepted — extends 0020; Kleinunternehmer 0 % for expenses superseded by 0022 |
| [0022](0022-kleinunternehmer-period-and-receipt-vat.md) | Kleinunternehmer exemption as a dated period; receipt VAT recorded, not deducted | Accepted — supersedes part of 0021 |
| [0023](0023-svs-kleinunternehmer-limits.md) | SVS Kleinunternehmer limits shown next to the VAT threshold | Accepted |
| [0024](0024-ui-language.md) | UI language: German source texts, English catalog | Accepted |
| [0025](0025-invoice-layout-numbers-and-payment-code.md) | Invoice layout, numbers per day, logo and payment QR code | Accepted |
| [0026](0026-invoice-drafts.md) | Invoice drafts: the form as typed, no number until issued | Accepted |
| [0027](0027-default-categories.md) | Default categories for a new installation | Accepted |
| [0028](0028-datev-optional.md) | DATEV can be switched off; off by default for new installations | Accepted |
| [0029](0029-year-archive.md) | A year archive (CSV, Belege, invoice PDFs) that opens without the app | Accepted |
| [0030](0030-restore-in-the-browser.md) | Restore in the browser, into a fresh installation | Accepted |
| [0031](0031-invoice-import.md) | Importing invoices from another tool, with the original PDF | Accepted |
| [0032](0032-bulk-invoice-import.md) | Bulk import of invoices: waiting list, one review, ticked ones imported | Accepted |
| [0033](0033-bulk-receipts.md) | Bulk upload of Belege: waiting list, review with preview, each booked with its Beleg | Accepted |
| [0034](0034-bank-data-csv-only.md) | Bank data stays CSV-only; CAMT.053 and EBICS deferred, PSD2 not possible | Accepted |
| [0035](0035-private-transfers.md) | Private transfers: bank lines marked private, outside the books | Accepted |
| [0036](0036-partial-payments.md) | Partial payments on invoices, split over the VAT rates | Accepted |
| [0037](0037-bewirtung-and-afa.md) | Deductible share (Bewirtung 50 %) and depreciation (AfA), one cost rule for the profit | Accepted |
| [0038](0038-recurring-invoices.md) | Recurring invoices: reminders and a pre-filled form, no automatic sending | Accepted |
| [0039](0039-email-notifications.md) | Email notifications to the owner via SMTP, credentials from the environment | Accepted; UI setup added by 0040 |
| [0040](0040-mail-server-in-the-web-ui.md) | Mail server set up in the web UI, password write-only and out of backups | Accepted |
| [0041](0041-business-share.md) | Business share of mixed-use expenses, per booking, lowering cost and Vorsteuer | Accepted |
| [0042](0042-background-receipt-reading.md) | Bulk Belege read in the background, several at once | Accepted |
| [0043](0043-change-description.md) | A booking's description can be changed, as an append-only history beside the booking | Accepted |
| [0044](0044-similar-bookings.md) | Suggestions from similar earlier bookings, for bank lines and Belege | Accepted |
| [0045](0045-change-business-share.md) | A booking's business share can be changed, as an append-only history | Accepted; amends 0041 |
