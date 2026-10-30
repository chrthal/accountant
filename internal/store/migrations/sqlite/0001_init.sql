-- Phase 0 has no domain tables yet; the real schema (contacts, entries,
-- append-only ledger, Konto/Gegenkonto tags) lands in Phase 1 as 0002_*.sql.
-- This migration exists so the mechanism itself is exercised and verified on
-- both engines before any real schema depends on it.
--
-- Keep this file and its postgres/ counterpart in lockstep: same table names,
-- same column names, same semantics, only the dialect-specific syntax differs.

CREATE TABLE IF NOT EXISTS app_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT OR IGNORE INTO app_meta (key, value) VALUES ('schema_origin', 'phase-0');
