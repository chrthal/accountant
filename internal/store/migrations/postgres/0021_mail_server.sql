-- ADR 0040: the mail server for notifications, set in the web UI. Keep in
-- lockstep with sqlite/0021_mail_server.sql.
--
-- One row. It holds a password, so backups and the year archive leave this
-- table out on purpose: after a restore the password is entered again.

CREATE TABLE mail_server (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    host       TEXT NOT NULL,
    port       INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    tls        TEXT NOT NULL CHECK (tls IN ('starttls', 'tls', 'none')),
    username   TEXT NOT NULL DEFAULT '',
    password   TEXT NOT NULL DEFAULT '',
    from_addr  TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
