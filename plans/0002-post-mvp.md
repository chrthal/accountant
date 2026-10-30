# Plan 0002: Post-MVP — redesign and the gaps the MVP left open

**Status:** Phases 6–12 built and verified; follow-up features listed below
**Date:** 2026-09-29

## Why this plan exists
Plan 0001 built every MVP feature, but "built" left a list of things that a
person using the app every day runs into within the first week. This plan starts
with a gap analysis: what is missing from the *plan*, and what the plan named as
a known limit without scheduling it. Then it schedules the fixes.

## Gap analysis

### A. Named in plan 0001 as a known limit, never scheduled
1. **Invoices never reach the ledger.** An issued invoice books no income, and
   the app cannot tell which invoices are paid. On a cash basis the income
   belongs on the day the payment arrives, so what is missing is a *payment* step,
   not a link at issue time. (plan 0001, Phase 2.)
2. **The invoice form has a fixed five rows.** (plan 0001, Phase 2.)
3. **Bank import: the `has_header` default is wrong for meinELBA**, and the first
   transaction is lost as column labels. Header detection should be automatic.
   (plan 0001, Phase 3.)
4. **Column mappings are not remembered**, so the same mapping has to be worked
   out again on every import. (plan 0001, Phase 3.)
5. **htmx was never added.** Everything works as plain form posts, which ADR 0001
   allowed "until an interaction needs partial updates". See ADR 0013.

### B. Missing from the plan entirely
6. **Receipts are not kept.** OCR reads the photo and throws it away. § 132 BAO
   requires Belege to be kept for seven years, and a booking without its Beleg
   is the first thing a Betriebsprüfung asks about. For most users this is the
   biggest missing feature.
7. **No VAT period report.** A business that is *not* a Kleinunternehmer files a
   USt-Voranmeldung per month or quarter. The app only has yearly totals.
8. **No per-category breakdown.** Categories exist but no report groups by them,
   so the E1a-style question "how much went on what" cannot be answered.
9. **No way to create a booking from a bank transaction.** Reconciling today
   means leaving the bank page, typing the booking by hand, coming back and
   matching it. For outgoings, the bank line usually *is* the booking.
10. **No match suggestions.** The match dropdown lists every open booking. The
    app knows amount and direction and could offer the matching ones first.
11. **A bank transaction can match only one booking.** A payment for a
    mixed-rate invoice is one transfer but two bookings (one per VAT rate), so it
    could never be reconciled. See ADR 0014.
12. **The ledger cannot be browsed.** The start page shows the last 50 bookings,
    with no filter, no search, no year and no detail view.
13. **Contacts and categories cannot be edited**, only created. Both are master
    data, not bookings; nothing requires them to be immutable.
14. **No protection against cross-site form posts (CSRF) and no login.** Plan
    0001 deferred auth as "multi-user complexity", but one password for one
    user is not multi-user, and a bookkeeping app reachable on a LAN should not
    accept a form post from any web page the user happens to have open.
15. **No explicit light/dark switch.** The CSS follows the OS setting only.
16. **The UI reads as a form collection rather than a finance tool:** no overview,
    no trend, top navigation that wraps into three lines on a phone.

### C. Bugs found while reading for this analysis
17. The OCR-prefilled booking form is rendered **without categories**
    (`renderPrefilledEntry` does not load them), so a receipt booking cannot get
    a category or its account defaults.
18. The settings page still says threshold monitoring "comes in a later stage",
    even though Phase 4 built it.
19. The booking list shows "Konto  / Gegenkonto " with empty values on every
    booking without account tags, which is most of them since ADR 0012.

### D. Considered and deliberately not scheduled
- **E-Rechnung (ebInterface / ZUGFeRD / XRechnung):** needed for invoices to the
  Austrian federal government (B2G) and increasingly asked for in B2B. It is a
  format of its own with its own validation, so it gets its own ADR if a real
  recipient asks for it.
- **Sending invoices by email:** that means SMTP credentials, deliverability and
  bounce handling. Download and attach is fine for now.
- **Stornorechnung / credit notes:** negative invoice lines break the
  `unit_price_cents >= 0` guarantee, and the required wording is a § 11 question
  in its own right. A wrong invoice is still corrected by issuing a new one.
- **Partial payments:** an invoice is paid in full or it is open. Partial
  payments change what "offen" means in every report, so they wait until a real
  case shows up.
- **Recurring bookings, multi-user, CAMT.053, FinanzOnline submission:** these
  stay out, as before.
- **Kennzahlen (KZ) on the VAT report:** the report shows the figures per rate,
  but it does *not* map them to U30 Kennzahlen. Getting that wrong produces a
  plausible-looking but incorrect return (CLAUDE.md), so the Steuerberater
  confirms the mapping first.

## Phases

### Phase 6: design system and app shell
A new design with light and dark themes and an explicit three-way switch
(System / Hell / Dunkel). App shell: sidebar on desktop, bottom tab bar on a
phone. A real overview page with KPI tiles, a monthly income/outgoings chart,
the Kleinunternehmer meter, and what needs attention (open invoices, unmatched
bank lines). The booking list moves to `/entries`. Still no JS build step: the
one script is a static file. Fixes C17–C19.

**Done when:** every page renders in both themes at phone and desktop width, the
switch persists across reloads, and the overview shows correct figures for a
seeded year.

### Phase 7: a ledger you can browse
`/entries` with year, month, direction, category and text filters plus
pagination. A booking detail page that shows the VAT split, accounts, contact,
category, the bank link and the Storno pair. Contacts and categories become
editable.

**Done when:** a booking can be found by text and by month, its detail page
shows its bank match, and renaming a category changes future defaults without
touching existing bookings.

### Phase 8: invoice lifecycle
Payment terms and a due date on each invoice. Status is offen, überfällig or
bezahlt. "Zahlung buchen" posts income entries using the invoice's own figures,
one per VAT rate, linked to the invoice. Positions can be added in the browser
(progressive enhancement, still a plain form without JS). An invoice detail page,
plus "als Vorlage verwenden" to start a new invoice from an old one.

**Done when:** a mixed-rate invoice is issued with more than five lines, shows as
overdue after its due date, is booked as paid (two entries whose sum is the
invoice gross, to the cent), and a Storno of one entry puts it back to open.

### Phase 9: reconciliation that does the work
Header auto-detection. The last mapping is remembered per column layout. Match
suggestions by amount, direction and date. "Buchung aus Umsatz" opens a
prefilled booking form and matches on save. "Rechnung bezahlt" books an open
invoice's payment and matches it in one step. A transaction can link to several
bookings whose sum equals its amount (ADR 0014).

**Done when:** a headerless meinELBA export imports, the second import
of the same layout needs no mapping choices, and one incoming transfer
reconciles a mixed-rate invoice.

### Phase 10: Belege
Receipt files (images, PDF) are stored with the booking in the database and are
append-only, like the booking. The OCR photo is kept automatically. A Beleg can
be added to an existing booking later, bookings without a Beleg are flagged, and
the files can be viewed and downloaded.

**Done when:** a receipt photo booked through OCR can be opened again from the
booking, and a booking without a Beleg is visibly marked in the list.

### Phase 11: reports
A VAT overview per month or quarter: output VAT per rate, input VAT (Vorsteuer),
and the resulting Zahllast or Gutschrift. No KZ mapping (see D). A per-category
breakdown for the year.

**Done when:** for a seeded quarter the figures match a hand calculation to the
cent, and Storno pairs are excluded.

### Phase 12: security baseline
Cross-origin form posts are refused (`http.CrossOriginProtection`, stdlib). An
optional single-user login via `BOOKKEEPER_PASSWORD` sets a signed session
cookie. Security headers include a CSP with `script-src 'self'`. ADR 0017.

**Done when:** a cross-origin POST is refused with 403, and with a password set
every page except `/healthz` and the static assets redirects to the login.

## Status of the phases
All seven phases are built. Each criterion is covered by tests on both engines
and was checked in the browser at desktop and phone width, in both themes.

- **Phase 6:** ADR 0013. Token-based stylesheet, System/Hell/Dunkel switch,
  sidebar and tab bar, overview with KPI tiles, an SVG monthly chart, the
  Kleinunternehmer meter and a to-do card. `app.js` replaces every inline
  script. C17–C19 fixed.
- **Phase 7:** `/entries` with filters, paging and sums; a booking detail page;
  editable contacts and categories.
- **Phase 8:** ADR 0015. Due date and IBAN snapshotted at issue; "Zahlung
  buchen" books one income entry per VAT rate; status derived from the ledger,
  so a Storno reopens the invoice; unlimited positions; "Als Vorlage".
- **Phase 9:** ADR 0014. `bank_matches` link table, column mapping guessed and
  remembered per layout, header detection, match suggestions, "Rechnung …
  bezahlt" and "Buchung anlegen" from a bank line.
- **Phase 10:** ADR 0016. Belege stored append-only in the database, the OCR
  photo kept, "Belege fehlen" on the overview and as a filter.
- **Phase 11:** VAT overview per quarter or month with an explicit not-a-UVA
  note and no U30 Kennzahlen; per-category breakdown.
- **Phase 12:** ADR 0017. `http.CrossOriginProtection`, a strict CSP, and an
  optional single-password login.

**Known limit:** text search is case-insensitive for umlauts only on Postgres.
SQLite's `LOWER` is ASCII-only. Server-level tests run on SQLite only; the store
tests carry the dialect coverage.

## Built after the phases
Each item has its ADR with the details.

- PDF invoice import for Belege: text layer via `pdftotext`, scans via
  `pdftoppm` + tesseract (ADR 0018).
- Podman verified as the container runtime for every `make` target.
- Backups: `bookkeeper backup|restore|verify`, daily automatic backups, a
  warning after 7 days without one (ADR 0019).
- Bank assistance: text split, invoice matching, "wie zuletzt", a review list
  of drafts, an optional local LLM via Ollama (ADR 0020).
- Category keywords and a default VAT rate for expenses (ADR 0021).
- Kleinunternehmer as a dated period; receipt VAT recorded, not deducted
  (ADR 0022).
- SVS Kleinunternehmer limits next to the VAT threshold (ADR 0023).
- Contacts can be deleted when unused; a date format setting.
- Language setting, German and English (ADR 0024).
- Invoice layout, numbers per day, logo, EPC payment QR code (ADR 0025).
- Invoices over several pages; invoice drafts with a PDF preview (ADR 0026).
- `CHANGELOG.md` drives the release notes; default categories (ADR 0027).
- DATEV can be switched off (ADR 0028).
- Year archive as a ZIP that opens without the app (ADR 0029).
- Restore in the browser into a fresh installation (ADR 0030).
- Invoice import for migrating from another tool, single and bulk
  (ADR 0031, ADR 0032).
- Bulk upload of Belege with duplicate detection (ADR 0033), read in the
  background (ADR 0042).
- Bank data stays CSV-only; CAMT.053 and EBICS deferred, PSD2 not possible
  (ADR 0034).
- Private transfers (ADR 0035), partial payments (ADR 0036), Bewirtung and AfA
  (ADR 0037), recurring invoices (ADR 0038).
- Email reminders to the owner, mail server set in the web UI
  (ADR 0039, ADR 0040).
- Business share of mixed-use expenses, changeable later (ADR 0041, ADR 0045).
- Changing a booking's description with its history kept (ADR 0043).
- Suggestions from similar earlier bookings (ADR 0044).
- A help page `/help` in German and English.
- Published on GitHub with a fresh history; CI and releases on GitHub Actions
  and ghcr.io (ADR 0005).

## Still open
- **A phone in hand** for the camera → OCR → Beleg path and the tab bar under a
  real notch (`safe-area-inset` is handled but not seen on a device).
- From plan 0001: the DATEV Prüfprogramm run and the Steuerberater's
  confirmation of accounts. Also for the Steuerberater: whether the VAT
  overview's booking-date (Istbesteuerung) assumption fits, and when Vorsteuer
  may be deducted.
- Possibly let the user correct a "wie zuletzt" template when a payee's booking
  changes.
- Read embedded e-invoice XML (ZUGFeRD / ebInterface) inside supplier PDFs.
- Encrypt backups for off-site storage.

**Next:** reverse charge on foreign services (§ 19 UStG), to clarify with
a Steuerberater first; dunning letters for overdue invoices.
