-- ADR 0014: a bank transaction can match several bookings. Keep in lockstep
-- with sqlite/0007_bank_matches.sql.
--
-- One transfer is sometimes several bookings. The clearest case is the
-- payment of a mixed-rate invoice, which ADR 0015 books as one entry per VAT
-- rate. The single matched_entry_id column could not express that, so the
-- link moves into its own table. An entry is still matched at most once
-- (UNIQUE entry_id), so no payment can count twice.
--
-- bank_transactions.matched_entry_id is carried over and then cleared. The
-- column stays, unused: removing a UNIQUE, referenced column needs a table
-- rebuild on SQLite (see 0005), which is not worth it for a column nothing
-- reads any more.

CREATE TABLE bank_matches (
    bank_transaction_id BIGINT NOT NULL REFERENCES bank_transactions (id),
    entry_id            BIGINT NOT NULL UNIQUE REFERENCES entries (id),
    PRIMARY KEY (bank_transaction_id, entry_id)
);

INSERT INTO bank_matches (bank_transaction_id, entry_id)
SELECT id, matched_entry_id FROM bank_transactions WHERE matched_entry_id IS NOT NULL;

UPDATE bank_transactions SET matched_entry_id = NULL WHERE matched_entry_id IS NOT NULL;
