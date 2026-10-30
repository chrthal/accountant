-- ADR 0033: Belege waiting to be booked in a bulk upload. Keep in lockstep
-- with postgres/0016_receipt_queue.sql. Same shape as invoice_import_queue,
-- and like it not part of the books: backups leave it out.

CREATE TABLE receipt_queue (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    filename   TEXT NOT NULL,
    data       BLOB NOT NULL,
    fields     TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL
);
