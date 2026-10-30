-- ADR 0043: a booking's description can be changed. Keep in lockstep with
-- postgres/0023_entry_descriptions.sql.
--
-- The booking itself stays as it was written (ADR 0007). Each change is a
-- new row here; the newest one is shown. Rows are append-only too, so the
-- original and every change stay visible (§ 131 Abs 1 Z 8 BAO).

CREATE TABLE entry_descriptions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    entry_id    INTEGER NOT NULL REFERENCES entries (id),
    description TEXT NOT NULL CHECK (description <> ''),
    changed_at  TIMESTAMP NOT NULL
);

CREATE INDEX entry_descriptions_entry_idx ON entry_descriptions (entry_id, id);

CREATE TRIGGER entry_descriptions_no_update BEFORE UPDATE ON entry_descriptions
BEGIN
    SELECT RAISE(ABORT, 'description changes are append-only: add a new one instead');
END;

CREATE TRIGGER entry_descriptions_no_delete BEFORE DELETE ON entry_descriptions
BEGIN
    SELECT RAISE(ABORT, 'description changes are append-only: add a new one instead');
END;
