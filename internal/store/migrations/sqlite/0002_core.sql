-- Phase 1 core schema (ADR 0007). Keep in lockstep with postgres/0002_core.sql:
-- same tables, same columns, same semantics — only syntax differs.
--
-- Money is integer cents. Dates are TEXT 'YYYY-MM-DD'. Entries are append-only,
-- enforced by the triggers at the bottom of this file.

CREATE TABLE contacts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT NOT NULL CHECK (kind IN ('customer', 'vendor', 'both')),
    name       TEXT NOT NULL,
    uid        TEXT NOT NULL DEFAULT '',   -- UID/ATU number, § 11 UStG where applicable
    address    TEXT NOT NULL DEFAULT '',
    email      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE categories (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    name               TEXT NOT NULL UNIQUE,
    direction          TEXT NOT NULL CHECK (direction IN ('income', 'outgoing', 'both')),
    -- Defaults only; empty until the user configures a chart of accounts with
    -- their Steuerberater. Deliberately not seeded (ADR 0007).
    default_konto      TEXT NOT NULL DEFAULT '',
    default_gegenkonto TEXT NOT NULL DEFAULT ''
);

CREATE TABLE entries (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    booked_on          TEXT NOT NULL,      -- 'YYYY-MM-DD', validated in Go
    direction          TEXT NOT NULL CHECK (direction IN ('income', 'outgoing')),
    description        TEXT NOT NULL,
    amount_gross_cents INTEGER NOT NULL CHECK (amount_gross_cents > 0),
    vat_rate           INTEGER NOT NULL CHECK (vat_rate IN (0, 10, 13, 20)),
    vat_cents          INTEGER NOT NULL CHECK (vat_cents >= 0),
    net_cents          INTEGER NOT NULL CHECK (net_cents > 0),
    -- Required: the DATEV export needs both on every booking (ADR 0003).
    konto              TEXT NOT NULL CHECK (konto <> ''),
    gegenkonto         TEXT NOT NULL CHECK (gegenkonto <> ''),
    contact_id         INTEGER REFERENCES contacts (id),
    category_id        INTEGER REFERENCES categories (id),
    -- A correction is a new row pointing at what it reverses; the original is
    -- never touched. Nothing is flagged on the original — it cannot be written.
    reverses_entry_id  INTEGER UNIQUE REFERENCES entries (id),
    created_at         TIMESTAMP NOT NULL
);

CREATE INDEX entries_booked_on_idx ON entries (booked_on);
CREATE INDEX entries_contact_idx   ON entries (contact_id);

-- Append-only, enforced by the database rather than by convention (ADR 0007).
CREATE TRIGGER entries_no_update BEFORE UPDATE ON entries
BEGIN
    SELECT RAISE(ABORT, 'entries are append-only: post a reversing entry instead');
END;

CREATE TRIGGER entries_no_delete BEFORE DELETE ON entries
BEGIN
    SELECT RAISE(ABORT, 'entries are append-only: post a reversing entry instead');
END;
