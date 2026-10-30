-- ADR 0026: invoice drafts. Keep in lockstep with
-- postgres/0013_invoice_drafts.sql.
--
-- A draft is the invoice form as typed, kept as JSON text: it may be
-- incomplete, and nothing in it is checked until it is issued. Unlike an
-- invoice it can be changed and deleted, and it has no number. There is no
-- foreign key to contacts, so a draft never blocks deleting a contact; a
-- draft whose contact is gone asks for a recipient again.

CREATE TABLE invoice_drafts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    data       TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
