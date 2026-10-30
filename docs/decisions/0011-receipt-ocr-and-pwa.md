# ADR 0011: Receipt OCR and PWA

**Date:** 2026-08-18
**Status:** Accepted

## Context
Phase 5 is the last of the MVP: photograph a receipt, get a pre-filled outgoing
booking to check and save, and have the app installable on a phone.

ADR 0002 already decided OCR runs locally via Tesseract rather than a cloud API,
and that its output is always a reviewed draft, never an auto-committed booking.
ADR 0004 already decided mobile means a responsive web app plus a PWA manifest,
not a native app. What is left to decide is how those attach to a single static
binary without undoing the deployment story.

## Decision

### Tesseract is shelled out to, and is optional

The app runs the `tesseract` binary as a subprocess on a temporary file. No cgo
binding: a binding would put a C dependency into a build that is deliberately
`CGO_ENABLED=0` (ADR 0005), costing the static binary for a feature used a few
times a week.

**OCR degrades rather than fails.** If the binary is absent the upload form still
works, the receipt is still attached to the draft, and the user types the figures
— with the UI saying why. This keeps `go run` and the SQLite dev path free of a
system dependency, and it means a deployment without Tesseract is a lesser
version of the app rather than a broken one.

The container image installs Tesseract with German and English data. That grows
it from ~27 MB to roughly 100 MB, which is the honest price of the feature.

Subprocess discipline: arguments are passed as an argv slice (never a shell
string), input goes through a temp file that is removed afterwards, the call runs
under a context timeout, and the upload is size-limited. The input is an
untrusted photo, so it gets treated as one.

### Extraction is heuristic, separated from the subprocess, and always a draft

`ocr.Extract` turns recognised text into a draft: total, date, vendor and, where
it can be read, the VAT rate. It is deliberately a small set of rules over the
words Austrian receipts actually use (`SUMME`, `GESAMT`, `ZU ZAHLEN`, `BRUTTO`,
`TOTAL`) rather than anything clever.

Splitting `Extract` from the Tesseract call is the same move as ADR 0008's
document/PDF split: the rules are tested exhaustively against realistic OCR text,
and the subprocess is a thin wrapper tested only when the binary exists.

Whatever it finds lands in the ordinary booking form, pre-filled and editable,
with the raw recognised text shown underneath so a wrong number is obvious.
Nothing is saved until the user submits. Fields it could not read stay empty
rather than being guessed at — an empty amount is a visible gap, a wrong amount
is not.

### PWA: manifest, icons, and a shell-only service worker

A `manifest.webmanifest`, two PNG icons, and a service worker that pre-caches the
stylesheet and icons and serves them cache-first. HTML is always fetched from the
network.

That last part is the decision. ADR 0004 already ruled offline data entry out of
scope, and a service worker that serves stale bookkeeping pages would be worse
than none: seeing yesterday's totals and not knowing it is a bookkeeping hazard.
So the worker makes the app installable and its shell fast, and nothing else.

Icons are generated once by a small program under `tools/` and committed as PNGs,
rather than generated at build time — no build step, per ADR 0001.

## Consequences
+ Receipt entry becomes a photo plus a glance, which is the point.
+ The app installs to a phone home screen and opens without browser chrome.
+ No cloud OCR, no data leaving the machine (ADR 0002).
+ The dev path keeps needing nothing but a Go toolchain.
- The image gets ~4× larger. Anyone who does not want that can run the app
  without Tesseract and lose only the pre-fill.
- Extraction quality on crumpled thermal paper will be mediocre. It is a draft
  that a person checks, so a bad read costs a correction, not a wrong booking.
- German recognition needs `tesseract-ocr-data-deu` present. Where only English
  data is installed, numbers and dates still read fine; German keywords may not.
- The service worker deliberately does nothing for offline use. Anyone expecting
  offline bookkeeping will be disappointed, and that expectation is wrong.

## Alternatives considered
- **A cgo Tesseract binding (gosseract):** rejected — it breaks the static
  binary for a feature used occasionally.
- **Cloud OCR (Google Vision, AWS Textract):** already rejected in ADR 0002;
  receipts are business records and this is a self-hosted tool.
- **Auto-committing high-confidence reads:** rejected. ADR 0002 is explicit that
  output is always reviewed, and a bookkeeping tool that files things by itself
  is a tool you cannot trust.
- **A service worker that caches pages for offline viewing:** rejected as above —
  stale financial figures presented as current.
