-- ADR 0039: email notifications. Keep in lockstep with
-- postgres/0020_notifications.sql.
--
-- One row per notification already mailed, so each event is mailed once.
-- Machine state, not books: backups leave it out.

CREATE TABLE notifications_sent (
    "key"   TEXT PRIMARY KEY,
    sent_at TIMESTAMP NOT NULL
);
