-- Postgres counterpart of migrations/sqlite/0001_init.sql. Same tables, same
-- columns, same semantics — only the dialect-specific syntax differs. Keep the
-- two files in lockstep; a divergence here is a bug that only shows up on one
-- deployment.
--
-- "key" is a non-reserved keyword in Postgres but quoted anyway for clarity.

CREATE TABLE IF NOT EXISTS app_meta (
    "key"  TEXT PRIMARY KEY,
    value  TEXT NOT NULL
);

INSERT INTO app_meta ("key", value) VALUES ('schema_origin', 'phase-0')
ON CONFLICT ("key") DO NOTHING;
