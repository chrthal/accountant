# 0029 — A year archive that opens without the app

## Context
The backup (ADR 0019) is a logical dump: complete and verified, but only
this app can read and restore it. The user pointed out that a backup which
needs the app to be read is not enough on its own. A tax adviser, an audit
or the user in ten years needs the books and the Belege in plain files.

## Decision
- `GET /export/archive?year=YYYY` sends one ZIP, `kmu-buchhaltung-YYYY/`:
  - `Buchungen-YYYY.csv`: the existing CSV export (ADR 0010), with a new
    "Belege" column that names each booking's files in the ZIP.
  - `Belege/`: every Beleg of the year's bookings, byte for byte as
    uploaded, named `date_Buchung-N_fileid_original-name`, so the folder
    sorts like the ledger.
  - `Rechnungen/`: every invoice issued in the year as PDF, drawn as the
    invoice page draws it.
  - `LIESMICH.txt`: what is where, and that the backup is what restores
    the app.
- The year follows the booking date for Belege and the issue date for
  invoices, as in the CSV and the VAT report.
- Files are read one at a time while the ZIP is written, so a year of
  receipts is never in memory at once. The plain CSV download gets the
  "Belege" column too, with the original file names.
- The archive does not replace the backup, and the backup does not replace
  the archive. Settings and reports both point to the other.

## Consequences
- The archive is for reading and passing on, not for restoring. Restoring
  stays the job of the backup.
- An invoice PDF is drawn when the archive is made, with the current logo
  (ADR 0025); its content is the stored, issued invoice.
- If writing fails halfway, the download ends without the ZIP's central
  directory, and unzip tools refuse it. It cannot pass as complete.

## Alternatives considered
- **Belege embedded in the CSV** (base64): unreadable for people.
- **One archive for all years**: grows without bound; tax work is per year.
