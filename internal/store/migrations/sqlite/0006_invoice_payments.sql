-- ADR 0015: invoice due dates and payments. Keep in lockstep with
-- postgres/0006_invoice_payments.sql.
--
-- SQLite allows ADD COLUMN with a REFERENCES clause as long as the default
-- is NULL, which it is here. No table rebuild is needed, and the append-only
-- triggers stay as they are.

ALTER TABLE invoices ADD COLUMN due_date    TEXT NOT NULL DEFAULT '';
ALTER TABLE invoices ADD COLUMN issuer_iban TEXT NOT NULL DEFAULT '';

ALTER TABLE entries ADD COLUMN invoice_id INTEGER REFERENCES invoices (id);
CREATE INDEX entries_invoice_idx ON entries (invoice_id);
