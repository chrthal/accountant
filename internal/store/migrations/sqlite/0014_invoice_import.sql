-- ADR 0031: invoices imported from another tool. Keep in lockstep with
-- postgres/0014_invoice_import.sql.
--
-- An imported invoice is an issued invoice like any other, but its document
-- is the original PDF, kept as it was sent. It is as immutable as the
-- invoice: added once, never replaced.

ALTER TABLE invoices ADD COLUMN imported INTEGER NOT NULL DEFAULT 0 CHECK (imported IN (0, 1));

CREATE TABLE invoice_originals (
    invoice_id   INTEGER PRIMARY KEY REFERENCES invoices (id),
    filename     TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL,
    size_bytes   INTEGER NOT NULL CHECK (size_bytes > 0),
    sha256       TEXT NOT NULL,
    data         BLOB NOT NULL,
    created_at   TIMESTAMP NOT NULL
);

CREATE TRIGGER invoice_originals_no_update BEFORE UPDATE ON invoice_originals
BEGIN
    SELECT RAISE(ABORT, 'invoice originals are kept, not replaced');
END;

CREATE TRIGGER invoice_originals_no_delete BEFORE DELETE ON invoice_originals
BEGIN
    SELECT RAISE(ABORT, 'invoice originals are kept, not replaced');
END;
