-- ADR 0025: invoice logo, BIC and footer contact. Keep in lockstep with
-- postgres/0012_invoice_branding.sql.
--
-- BIC and contact are snapshotted like the other issuer details (ADR 0008).
-- The logo is branding, not a § 11 field: there is one, and every invoice
-- PDF uses the current one.

ALTER TABLE invoices ADD COLUMN issuer_bic     TEXT NOT NULL DEFAULT '';
ALTER TABLE invoices ADD COLUMN issuer_contact TEXT NOT NULL DEFAULT '';

CREATE TABLE logo (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    content_type TEXT NOT NULL CHECK (content_type IN ('image/png', 'image/jpeg')),
    data         BLOB NOT NULL,
    updated_at   TIMESTAMP NOT NULL
);
