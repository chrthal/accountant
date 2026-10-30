-- ADR 0045: a booking's business share (ADR 0041) can be changed. Keep in
-- lockstep with sqlite/0024_entry_shares.sql.
--
-- As with the description (ADR 0043), the booking stays as written and each
-- change is a new append-only row; the newest one counts.

CREATE TABLE entry_shares (
    id            BIGSERIAL PRIMARY KEY,
    entry_id      BIGINT NOT NULL REFERENCES entries (id),
    business_pct  INTEGER NOT NULL CHECK (business_pct BETWEEN 1 AND 100),
    business_note TEXT NOT NULL DEFAULT '',
    changed_at    TIMESTAMP NOT NULL
);

CREATE INDEX entry_shares_entry_idx ON entry_shares (entry_id, id);

CREATE FUNCTION entry_shares_append_only() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'share changes are append-only: add a new one instead';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER entry_shares_no_update BEFORE UPDATE ON entry_shares
    FOR EACH ROW EXECUTE FUNCTION entry_shares_append_only();

CREATE TRIGGER entry_shares_no_delete BEFORE DELETE ON entry_shares
    FOR EACH ROW EXECUTE FUNCTION entry_shares_append_only();
