-- ADR 0015: invoice due dates and payments. Keep in lockstep with
-- sqlite/0006_invoice_payments.sql.
--
-- Invoices stay immutable. Both new invoice columns are written once, at
-- issue time, and invoices issued before this migration simply have none. A
-- payment is not a change to the invoice. It is an ordinary income entry that
-- points at the invoice, so it follows the ledger's append-only rules and is
-- undone the ledger's way, by a Storno.
--
-- ADD COLUMN with a constant default rewrites no rows and fires no row
-- triggers, so the immutability triggers do not object.

ALTER TABLE invoices ADD COLUMN due_date    TEXT NOT NULL DEFAULT '';
ALTER TABLE invoices ADD COLUMN issuer_iban TEXT NOT NULL DEFAULT '';

ALTER TABLE entries ADD COLUMN invoice_id BIGINT REFERENCES invoices (id);
CREATE INDEX entries_invoice_idx ON entries (invoice_id);
