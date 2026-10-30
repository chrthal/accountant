# ADR 0018: Supplier PDF invoices: text layer first, OCR for scans

**Date:** 2026-09-30
**Status:** Accepted. Extends ADR 0011.

## Context
"Beleg erfassen" only accepted photos (`accept="image/*"`), and tesseract
cannot read a PDF. Most supplier invoices for a small business arrive as PDFs
by email: hosting, software, telephone, the Steuerberater's own bill. Those
had to be typed by hand and attached separately, which is exactly the manual
work the Beleg step exists to save.

Two kinds of PDF arrive:

- **Generated PDFs** from invoicing software. These carry a text layer, and
  reading it is exact. OCR over a rendering of the same page would only add
  misreads.
- **Scanned PDFs** (a scanner app, "print to PDF" of a photo). These have no
  text, or only a few stray characters.

## Decision

### poppler's command-line tools, as subprocesses
- `pdftotext -layout -enc UTF-8 -f 1 -l 3` reads the text layer of the first
  three pages. `-layout` keeps a label and its amount on one line
  ("Gesamtbetrag 120,00 EUR"), which is what the extraction rules look for.
  Three pages are enough, since totals and dates are at the front and the
  terms and conditions at the back.
- If that yields fewer than 40 letters and digits, the PDF is treated as a
  scan. `pdftoppm -r 300 -png -singlefile` renders page 1, and the image goes
  through the existing tesseract path.

The same rules as ADR 0011 apply: argv only (never a shell), a temp directory
that is removed afterwards, the 30-second timeout, and the 10 MiB cap. The
file type is decided by the `%PDF-` magic bytes, never by the name.

Poppler is the standard PDF toolkit on every Linux distribution: boring, and
well understood. A pure-Go PDF text library was the alternative; see below.

### All optional, and the upload always works
Without poppler a PDF is still accepted and **stored as the Beleg** (ADR
0016); only the pre-fill is missing, and the page says so. The same now holds
for photos without tesseract: until now the upload form was hidden
entirely when OCR was missing, which meant a Beleg could not go through this
page at all.

### Extraction learned invoices, not only till receipts
`ocr.Extract` is shared by both paths and was built on till receipts. Invoices
needed four more rules, each covered by a test:

- **Subtotals are not the total.** Lines with "netto", "USt", "MwSt" or
  "Zwischensumme" are skipped when looking for the amount, unless they also
  say "brutto" or "zu zahlen".
- **The invoice's own date.** A date next to "Rechnungsdatum" or "Datum" wins
  over the first date on the page, which is often a delivery date.
- **The vendor is not the recipient.** The user's own business name (from
  settings) is excluded, as are address, contact and bank lines. `-layout`
  columns are split at gaps of three or more spaces, so a letterhead printed
  beside "RECHNUNG" is still found.
- **The supplier's invoice number** ("Rechnungsnummer: RE-2026/0815",
  "Rechnung Nr. 4711", or an unlabelled "Rechnung 2026-0001" title line)
  goes into the booking text, so the booking can be traced to the paper.

The result is still only a draft. Every field stays editable, the recognised
text is shown beside the form with how it was obtained ("Textebene der PDF" or
OCR), and nothing is saved until the user submits.

## Consequences
- The container image gains `poppler-utils`: 82 MB → 86 MB.
- CI's containerised OCR step installs poppler too. Otherwise the PDF tests
  would skip themselves and the feature would be untested.
- Tests generate their PDFs at run time with the `fpdf` dependency the app
  already has: one with a text layer, and one containing only the receipt
  photo (a scan). One more fixture is the `pdftotext -layout` output of an
  invoice from this app's own PDF writer. It was the first real-world check,
  and it caught the unlabelled invoice number.
- An uploaded PDF is parsed by poppler, which is C code reading untrusted
  input. That is the same class of risk as tesseract reading a photo, and it is
  bounded the same way: a separate process, a timeout, a size cap, and no
  shell.
- Not handled: invoice line items (the booking is one amount at one rate),
  mixed-rate invoices (the first rate found is proposed, and the user splits
  it into two bookings), and e-invoice XML (ebInterface / ZUGFeRD attachments
  inside the PDF). The last would be exact data rather than heuristics, and
  is the natural next step if suppliers send it.

## Alternatives considered
- **A pure-Go PDF library (e.g. `ledongthuc/pdf`):** no system dependency, but
  text extraction from real-world PDFs (compressed streams, CID fonts,
  ligatures) is where such libraries are weakest. It would also cover only
  half the problem, since scans still need rendering.
- **Rendering every PDF and OCR'ing it:** simpler code, but worse results on
  the common case, where the exact text was right there.
- **Parsing embedded e-invoice XML:** exact, but few Austrian small-business
  suppliers attach it yet. It is noted above as the follow-up.
