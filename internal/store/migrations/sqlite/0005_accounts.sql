-- ADR 0012: Konto/Gegenkonto stop being required on every entry and are
-- resolved from the booking's category instead. Keep in lockstep with
-- postgres/0005_accounts.sql.
--
-- SQLite cannot drop a CHECK constraint, so this is the documented table
-- rebuild (sqlite.org/lang_altertable.html#otheralter): create, copy, drop,
-- rename. Foreign-key enforcement is off for the duration — the migrator turns
-- it off around every SQLite migration and runs a foreign_key_check afterwards
-- (see Store.migrateSQLite), because dropping a table that bank_transactions
-- and entries themselves point at is otherwise refused.
--
-- ids are carried over verbatim, so every reference still resolves. DROP TABLE
-- fires no triggers, so the append-only triggers do not block the rebuild —
-- they go with the old table and are recreated at the bottom.

CREATE TABLE entries_new (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    booked_on          TEXT NOT NULL,
    direction          TEXT NOT NULL CHECK (direction IN ('income', 'outgoing')),
    description        TEXT NOT NULL,
    amount_gross_cents INTEGER NOT NULL CHECK (amount_gross_cents > 0),
    vat_rate           INTEGER NOT NULL CHECK (vat_rate IN (0, 10, 13, 20)),
    vat_cents          INTEGER NOT NULL CHECK (vat_cents >= 0),
    net_cents          INTEGER NOT NULL CHECK (net_cents > 0),
    -- Optional now (ADR 0012): blank until a category supplies them, and the
    -- DATEV export refuses to run over a year that still has blanks.
    konto              TEXT NOT NULL DEFAULT '',
    gegenkonto         TEXT NOT NULL DEFAULT '',
    contact_id         INTEGER REFERENCES contacts (id),
    category_id        INTEGER REFERENCES categories (id),
    reverses_entry_id  INTEGER UNIQUE REFERENCES entries (id),
    created_at         TIMESTAMP NOT NULL
);

INSERT INTO entries_new
    (id, booked_on, direction, description, amount_gross_cents, vat_rate,
     vat_cents, net_cents, konto, gegenkonto, contact_id, category_id,
     reverses_entry_id, created_at)
SELECT
     id, booked_on, direction, description, amount_gross_cents, vat_rate,
     vat_cents, net_cents, konto, gegenkonto, contact_id, category_id,
     reverses_entry_id, created_at
FROM entries
ORDER BY id;

DROP TABLE entries;

ALTER TABLE entries_new RENAME TO entries;

CREATE INDEX entries_booked_on_idx ON entries (booked_on);
CREATE INDEX entries_contact_idx   ON entries (contact_id);

CREATE TRIGGER entries_no_update BEFORE UPDATE ON entries
BEGIN
    SELECT RAISE(ABORT, 'entries are append-only: post a reversing entry instead');
END;

CREATE TRIGGER entries_no_delete BEFORE DELETE ON entries
BEGIN
    SELECT RAISE(ABORT, 'entries are append-only: post a reversing entry instead');
END;
