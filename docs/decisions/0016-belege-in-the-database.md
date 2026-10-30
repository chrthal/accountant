# ADR 0016: Belege (receipts) stored in the database, append-only

**Date:** 2026-09-29
**Status:** Accepted

## Context
The OCR step (ADR 0011) read a receipt photo and discarded it. § 132 BAO
requires Belege to be kept for seven years, and "where is the Beleg for this
booking?" is the first question in any Betriebsprüfung. Plan 0002 lists this
as the biggest missing feature.

The container is stateless by design (ADR 0005): state lives in Postgres or in
the SQLite file.

## Decision

### Files live in the database
Table `attachments (entry_id, filename, content_type, size_bytes, sha256,
data)`, with `BYTEA` on Postgres and `BLOB` on SQLite. A database backup is
therefore a backup of the books *and* their evidence, and the stateless
container stays stateless. Sizes are small: a phone photo is 2–5 MiB, the
limit is 10 MiB, and a year of a small business's receipts is a few hundred
MiB at most.

### Append-only, like the booking
Triggers refuse `UPDATE` and `DELETE` on `attachments`, the same mechanism
ADR 0007 uses for entries. A Beleg can be **added** to an existing booking
(adding evidence later is normal, for example an emailed invoice for a card
payment), but never replaced or removed. The SHA-256 is stored, so a file's
integrity can be shown.

### The type is sniffed, never trusted
`http.DetectContentType` on the bytes decides the type. Accepted: JPEG, PNG,
WebP, GIF, PDF, and HEIC (recognised by its ISO-BMFF brand, which the stdlib
does not sniff). Everything else is refused. Served files carry
`X-Content-Type-Options: nosniff` and a `sandbox` CSP, so even a crafted file
cannot run as a page on the app's origin.

### The OCR photo is kept automatically
The photo travels from the OCR step to the booking form as a hidden field
(the same carry-it-through-the-form approach ADR 0009 uses for bank files, so
an abandoned draft leaves nothing on the server) and is stored when the
booking is saved. The booking form also accepts a file directly.

The file is validated **before** the booking is written, so a rejected file
never leaves a booking without its receipt.

### Missing Belege are visible
Effective outgoing bookings without an attachment are counted on the overview
("Belege fehlen") and can be listed (`/entries?beleg=fehlt`). Income is left
out, because its Beleg is the issued invoice, which the app already keeps.

## Consequences
- Database size grows with receipts. Postgres handles this without trouble;
  for SQLite it is one bigger file on the volume.
- A booking form post can be up to about 16 MiB (a 10 MiB file base64-encoded
  from the OCR step). The server bounds the body at that.
- HEIC photos are stored but not shown as a thumbnail, since browsers mostly
  cannot render them. They download instead.
- The exports do not include the files. A Betriebsprüfung export (for example
  a ZIP of Belege per year) is a later step if one is asked for.

## Alternatives considered
- **Files on disk / a volume:** this breaks the stateless container, gives
  two things to back up, and makes append-only a convention instead of a
  constraint.
- **Object storage (S3/MinIO):** a new moving part and credentials for a
  handful of files a week.
- **Keep only the OCR text:** that is not the Beleg.
