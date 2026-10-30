# 0042 — Bulk Belege read in the background, several at once

## Context
The bulk upload of Belege (ADR 0033) read every file inside the upload
request, one after the other: tesseract for photos, pdftotext or pdftoppm
plus tesseract for PDFs. A phone photo takes several seconds, so an upload
of many files kept the browser waiting for minutes with no sign of life.
The user asked for it to be faster, or at least to say that it takes time.

## Decision
- **The upload only stores.** Each file goes onto the waiting list with a
  `pending` mark, the file's hash and nothing read yet. The request ends as
  soon as the files are sent.
- **A background reader** (`startReading`) reads the pending rows with up to
  four workers (fewer on fewer cores) and replaces the mark with what it
  read, the same proposal as before. One run at a time; an upload during a
  run makes it look again at the end. A restart stops a run; opening the
  waiting list starts it again.
- **One tesseract thread per run** (`OMP_THREAD_LIMIT=1` via
  `ocr.SingleThreaded`). Tesseract's own threads only pay off when it runs
  alone; several runs that each use every core are slower. A single
  receipt in the normal form still uses all threads.
- **The waiting list shows progress.** Pending rows show "wird gelesen …"
  and can only be discarded. While any are pending, the page reloads every
  four seconds, unless the user has typed something; then it offers a
  reload link instead, so no input is lost.
- **The upload form says so.** A note that many files take time, and the
  button changes to "Wird hochgeladen …" while the files are sent.

## Consequences
- Measured in the container (5 cores) with 12 photos: the upload answered
  in 0.1 s and all were read in 2.7 s. Eight runs of tesseract on the same
  photo took 5 s one after the other and 2 s four at a time.
- Sending the files still takes as long as the network needs; only the
  reading moved out of the request.
- The reader holds one file per worker in memory, not the whole upload.

## Alternatives considered
- **Read in parallel inside the request:** faster, but the browser still
  waits with no sign of life, and a large upload would sit in memory.
- **Scale large photos down before OCR:** fewer pixels, faster tesseract,
  but small print on a receipt photographed from afar could get lost.
  Revisit if reading is still too slow on real photos.
- **A progress bar via JavaScript polling:** more code for the same
  information; a reload every few seconds is enough, and the app works
  without the script.
