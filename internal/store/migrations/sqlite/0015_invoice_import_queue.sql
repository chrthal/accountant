-- ADR 0032: invoices waiting for review in a bulk import. Keep in lockstep
-- with postgres/0015_invoice_import_queue.sql.
--
-- A row is an uploaded original and what was read from it, as text, until
-- the user imports or discards it. It is not part of the books: backups
-- leave it out, and the originals are still on the user's disk.

CREATE TABLE invoice_import_queue (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    filename   TEXT NOT NULL,
    data       BLOB NOT NULL,
    fields     TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL
);
