# ADR 0002: sevdesk feature scan — scope adjustments

**Date:** 2026-08-17
**Status:** Accepted

## Context
Scanned sevdesk.at (established Austrian/German bookkeeping SaaS) to sanity-check
MVP scope against a mature product in the same space. Full feature set: invoices/quotes,
receipt capture with OCR and auto-categorization, bank sync across 4,000+ banks with
auto-matching, contacts/CRM, product/stock management, real-time dashboard, EÜR/GuV,
automatic USt-Voranmeldung, DATEV/ADDISON export, Steuerberater portal, 60+ integrations,
mobile app, GoBD-compliant revision-proof logging.

Most of this is SaaS-scale and out of scope by design (see docs/decisions/0001).
Three items were worth a deliberate decision rather than silent omission.

## Decisions

**1. Receipt/Beleg OCR — in scope, as Phase 5, local-only**
Reduces manual entry for outgoings meaningfully. Implemented via a locally-run
Tesseract binary (shelled out from Go, or a thin binding) rather than a cloud
OCR API — keeps the tool self-hostable with no external data flow, consistent
with the project's homelab/self-hosting orientation. Accuracy expectations are
modest: extract amount/date/vendor as a pre-filled draft, always reviewed by
the user before saving, never auto-committed.

**2. Append-only booking ledger — in scope, from Phase 1**
Austria's record-keeping obligation (§ 131 BAO, "Grundsätze ordnungsgemäßer
Buchführung") has the same underlying intent as Germany's GoBD: bookings
shouldn't be silently editable after the fact. Cheap to design in from day
one (corrections are new offsetting entries, not in-place edits), expensive
to retrofit onto an existing schema later. No separate audit-log table needed
initially — the ledger's own append-only discipline is the audit trail.

**3. Steuerberater export — structured CSV, not DATEV/ADDISON format**
sevdesk's DATEV/ADDISON export exists because it integrates with the
Steuerberater's own software ecosystem at SaaS scale. For a single small
business, a clean CSV (bookings + VAT summary) handed to the Steuerberater
is very likely sufficient — DATEV format has real complexity (account
frameworks, format quirks) that isn't worth building speculatively.

## Consequences
+ Ledger design decision is made once, correctly, before any schema exists
+ OCR stays self-hosted, no third-party data-sharing concern
+ Avoids speculative DATEV complexity
- OCR accuracy will be lower than a commercial cloud OCR service — acceptable
  since output is always a reviewed draft, not an auto-committed booking
- If a real Steuerberater later insists on DATEV format, that's a new ADR and
  a real (not speculative) requirement to build against

## Open item
Confirm with the actual Steuerberater, once one is in the loop, whether the
CSV export format is acceptable — don't guess at their requirements further
than this.
