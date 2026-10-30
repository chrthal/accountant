-- ADR 0016: receipts (Belege) stored with their booking. Keep in lockstep
-- with postgres/0008_attachments.sql.
--
-- § 132 BAO requires Belege to be kept for seven years, so an attachment is
-- as append-only as the booking it documents: it can be added, never
-- replaced or removed. The file lives in the database, next to the booking,
-- so a backup of the database is a backup of the books (the container stays
-- stateless, ADR 0005).

CREATE TABLE attachments (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    entry_id     INTEGER NOT NULL REFERENCES entries (id),
    filename     TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL,
    size_bytes   INTEGER NOT NULL CHECK (size_bytes > 0),
    sha256       TEXT NOT NULL,
    data         BLOB NOT NULL,
    created_at   TIMESTAMP NOT NULL
);

CREATE INDEX attachments_entry_idx ON attachments (entry_id);

CREATE TRIGGER attachments_no_update BEFORE UPDATE ON attachments
BEGIN
    SELECT RAISE(ABORT, 'attachments are append-only: Belege are kept, not replaced');
END;

CREATE TRIGGER attachments_no_delete BEFORE DELETE ON attachments
BEGIN
    SELECT RAISE(ABORT, 'attachments are append-only: Belege are kept, not replaced');
END;
