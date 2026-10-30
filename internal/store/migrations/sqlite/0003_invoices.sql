-- Phase 2: settings and invoices (ADR 0008). Keep in lockstep with
-- postgres/0003_invoices.sql.

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE invoices (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    year               INTEGER NOT NULL,
    seq                INTEGER NOT NULL,
    number             TEXT NOT NULL UNIQUE,   -- rendered 'YYYY-NNNN', derived from year/seq
    issue_date         TEXT NOT NULL,          -- 'YYYY-MM-DD'
    service_date       TEXT NOT NULL,          -- Liefer-/Leistungsdatum, § 11 Abs 1 Z 4
    contact_id         INTEGER NOT NULL REFERENCES contacts (id),

    -- Recipient and issuer are snapshotted: an invoice must keep showing the
    -- parties as they were the day it was issued (ADR 0008).
    recipient_name     TEXT NOT NULL,
    recipient_address  TEXT NOT NULL DEFAULT '',
    recipient_uid      TEXT NOT NULL DEFAULT '',
    issuer_name        TEXT NOT NULL,
    issuer_address     TEXT NOT NULL DEFAULT '',
    issuer_uid         TEXT NOT NULL DEFAULT '',

    kleinunternehmer   INTEGER NOT NULL CHECK (kleinunternehmer IN (0, 1)),
    note               TEXT NOT NULL DEFAULT '',
    net_cents          INTEGER NOT NULL,
    vat_cents          INTEGER NOT NULL,
    gross_cents        INTEGER NOT NULL,
    created_at         TIMESTAMP NOT NULL,

    UNIQUE (year, seq)
);

CREATE TABLE invoice_lines (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    invoice_id     INTEGER NOT NULL REFERENCES invoices (id),
    position       INTEGER NOT NULL,
    description    TEXT NOT NULL,
    -- Quantity in thousandths, so 1,5 Stunden is 1500 and no float is involved.
    quantity_milli INTEGER NOT NULL CHECK (quantity_milli > 0),
    unit           TEXT NOT NULL DEFAULT '',
    unit_price_cents INTEGER NOT NULL CHECK (unit_price_cents >= 0),
    vat_rate       INTEGER NOT NULL CHECK (vat_rate IN (0, 10, 13, 20)),
    net_cents      INTEGER NOT NULL,
    vat_cents      INTEGER NOT NULL,
    gross_cents    INTEGER NOT NULL,

    UNIQUE (invoice_id, position)
);

CREATE INDEX invoice_lines_invoice_idx ON invoice_lines (invoice_id);

-- Issued invoices are documents, not drafts: once written they are as immutable
-- as ledger entries (ADR 0007, ADR 0008). A wrong invoice is corrected by
-- issuing a new one, exactly as it would be on paper.
CREATE TRIGGER invoices_no_update BEFORE UPDATE ON invoices
BEGIN
    SELECT RAISE(ABORT, 'invoices are immutable: issue a corrected invoice instead');
END;

CREATE TRIGGER invoices_no_delete BEFORE DELETE ON invoices
BEGIN
    SELECT RAISE(ABORT, 'invoices are immutable: issue a corrected invoice instead');
END;

CREATE TRIGGER invoice_lines_no_update BEFORE UPDATE ON invoice_lines
BEGIN
    SELECT RAISE(ABORT, 'invoice lines are immutable: issue a corrected invoice instead');
END;

CREATE TRIGGER invoice_lines_no_delete BEFORE DELETE ON invoice_lines
BEGIN
    SELECT RAISE(ABORT, 'invoice lines are immutable: issue a corrected invoice instead');
END;
