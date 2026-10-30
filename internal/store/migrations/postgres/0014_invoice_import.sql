-- ADR 0031: invoices imported from another tool. Keep in lockstep with
-- sqlite/0014_invoice_import.sql.
--
-- An imported invoice is an issued invoice like any other, but its document
-- is the original PDF, kept as it was sent. It is as immutable as the
-- invoice: added once, never replaced.

ALTER TABLE invoices ADD COLUMN imported INTEGER NOT NULL DEFAULT 0 CHECK (imported IN (0, 1));

CREATE TABLE invoice_originals (
    invoice_id   BIGINT PRIMARY KEY REFERENCES invoices (id),
    filename     TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL CHECK (size_bytes > 0),
    sha256       TEXT NOT NULL,
    data         BYTEA NOT NULL,
    created_at   TIMESTAMP NOT NULL
);

CREATE FUNCTION invoice_originals_append_only() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'invoice originals are kept, not replaced';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER invoice_originals_no_update BEFORE UPDATE ON invoice_originals
    FOR EACH ROW EXECUTE FUNCTION invoice_originals_append_only();
CREATE TRIGGER invoice_originals_no_delete BEFORE DELETE ON invoice_originals
    FOR EACH ROW EXECUTE FUNCTION invoice_originals_append_only();
