# ADR 0001: Initial tech stack

**Date:** 2026-08-17
**Status:** Accepted — the database decision is superseded by ADR 0006
(PostgreSQL primary, SQLite retained for dev/test), and htmx by ADR 0013 (one
small vanilla script, no htmx until a partial update needs it). Everything else
stands.

## Context
Need a bookkeeping tool for a small Austrian business: income/outgoings
tracking, bank transaction import, invoice generation, Kleinunternehmerregelung
and multi-rate VAT support. Explicit constraint from the outset: Go backend,
frontend is flexible, but the whole thing should be as simple as possible.

## Decision
- Go, standard library `net/http` (add `chi` only if plain mux gets awkward)
- SQLite via `modernc.org/sqlite` — pure Go, no cgo, single-file DB
- Server-rendered `html/template` + htmx; no SPA framework, no JS build step
- `gofpdf` / `maroto` for PDF invoice generation
- CSV as the first bank-import format; CAMT.053 XML deferred

## Consequences
**Positive**
- Ships as a single static binary + one SQLite file — trivial to back up,
  trivial to self-host (fits existing homelab/k8s infra if it ever needs to
  move there)
- No separate frontend build pipeline or node toolchain to maintain
- Small surface area, easy for one person to reason about the whole system

**Negative / trade-offs**
- Less rich client-side interactivity than a full SPA — acceptable for an
  internal bookkeeping tool
- SQLite limits concurrent writers — fine for single-user/small-team use;
  would need a Postgres migration if usage or concurrency grows materially

## Alternatives considered
- **React/Vue SPA + REST API:** more moving parts (build step, API versioning,
  client state management) for no real benefit at this scale — rejected.
- **Postgres from day one:** deferred until there's an actual concurrency or
  multi-instance need; premature for a single-user tool.
