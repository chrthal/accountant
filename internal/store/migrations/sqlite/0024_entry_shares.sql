-- ADR 0045: a booking's business share (ADR 0041) can be changed. Keep in
-- lockstep with postgres/0024_entry_shares.sql.
--
-- As with the description (ADR 0043), the booking stays as written and each
-- change is a new append-only row; the newest one counts.

CREATE TABLE entry_shares (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    entry_id      INTEGER NOT NULL REFERENCES entries (id),
    business_pct  INTEGER NOT NULL CHECK (business_pct BETWEEN 1 AND 100),
    business_note TEXT NOT NULL DEFAULT '',
    changed_at    TIMESTAMP NOT NULL
);

CREATE INDEX entry_shares_entry_idx ON entry_shares (entry_id, id);

CREATE TRIGGER entry_shares_no_update BEFORE UPDATE ON entry_shares
BEGIN
    SELECT RAISE(ABORT, 'share changes are append-only: add a new one instead');
END;

CREATE TRIGGER entry_shares_no_delete BEFORE DELETE ON entry_shares
BEGIN
    SELECT RAISE(ABORT, 'share changes are append-only: add a new one instead');
END;
