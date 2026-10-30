-- ADR 0020: booking suggestions from the optional local LLM. Keep in lockstep
-- with sqlite/0010_bank_suggestions.sql.
--
-- A cache, not part of the books. A suggestion is a proposal the user may or
-- may not accept, and it can always be produced again. It is therefore left
-- out of backups on purpose (store.derivedTables), and rows may be replaced
-- and deleted freely.

CREATE TABLE bank_suggestions (
    bank_transaction_id BIGINT PRIMARY KEY REFERENCES bank_transactions (id),
    description         TEXT NOT NULL DEFAULT '',
    category_id         BIGINT REFERENCES categories (id),
    contact_id          BIGINT REFERENCES contacts (id),
    vat_rate            INTEGER,
    unsure              INTEGER NOT NULL DEFAULT 0,  -- the model said "low confidence"
    model               TEXT NOT NULL,
    created_at          TIMESTAMP NOT NULL
);
