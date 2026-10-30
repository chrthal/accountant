-- ADR 0012: Konto/Gegenkonto stop being required on every entry and are
-- resolved from the booking's category instead. Keep in lockstep with
-- sqlite/0005_accounts.sql.
--
-- The columns stay NOT NULL DEFAULT '' — only the "must not be empty" checks
-- go. Postgres names a column CHECK constraint <table>_<column>_check.

ALTER TABLE entries DROP CONSTRAINT entries_konto_check;
ALTER TABLE entries DROP CONSTRAINT entries_gegenkonto_check;
