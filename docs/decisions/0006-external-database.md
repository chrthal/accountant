# ADR 0006: PostgreSQL as the primary database, SQLite retained for dev

**Date:** 2026-08-18
**Status:** Accepted — supersedes ADR 0001's database choice

## Context
ADR 0001 chose SQLite via `modernc.org/sqlite` as the only database: one file,
trivial backup, no server. It also named the exit: "would need a Postgres
migration if usage or concurrency grows materially."

Running against an external database is now an explicit requirement, not a
hypothetical. The reasons an embedded file stops being sufficient are ordinary
ones: the database wants to live somewhere backed up, monitored, and restorable
independently of the app container; a container that owns its own storage is
awkward to schedule anywhere (the deploy target is still open, ADR 0005); and
SQLite's single-writer limit is a ceiling that has to be removed before it is
hit, not after.

Deciding this in Phase 0, before any domain schema exists, costs almost nothing.
Deciding it after Phase 1's ledger, invoice, and DATEV tables are written would
mean porting a schema that was authored against one dialect's assumptions.

## Decision

**PostgreSQL is the primary engine.** A real deployment runs Postgres; that is
what `compose.yaml` starts and what the deployment documentation describes.

**SQLite is retained as the development and test path**, not dropped. It is what
makes `make dev` a single command with no server to start, and what keeps the
test suite fast and dependency-free. It remains a legitimate way to run the app
for a single user who wants the one-file story of ADR 0001.

**Selection is by DSN**, one setting, no build tags:

```
BOOKKEEPER_DSN=postgres://user:pass@host:5432/bookkeeper?sslmode=disable
BOOKKEEPER_DSN=sqlite://bookkeeper.db     # or a bare path
```

Anything else is rejected at startup with a message naming the supported
schemes, rather than being silently misinterpreted.

**Portability is hand-maintained, not abstracted away.** No ORM, no query
builder, no code generation — that would be a large dependency and a build step,
against the project's core constraint. Instead:

- All access goes through `database/sql`.
- Queries are written once in the dialect-portable subset with `?` placeholders
  and passed through `Store.Rebind`, which rewrites them to `$1, $2, …` on
  Postgres.
- Migrations are per-dialect files, `migrations/postgres/NNNN_*.sql` and
  `migrations/sqlite/NNNN_*.sql`, applied in filename order and recorded in
  `schema_migrations`. Same table names, same column names, same semantics in
  both; only syntax differs (`INSERT OR IGNORE` vs. `ON CONFLICT DO NOTHING`).
- The test suite runs against both engines. Postgres cases activate when
  `TEST_POSTGRES_DSN` is set — CI always sets it from a service container, so
  the two paths cannot quietly diverge.

**pgx (`github.com/jackc/pgx/v5/stdlib`)** is the driver: pure Go, so
`CGO_ENABLED=0` and the static-binary story of ADR 0005 survive.

Postgres connections default to pgx's **simple query protocol** unless the DSN
says otherwise. pgx's default extended protocol rejects multi-statement strings,
which is exactly what a migration file is; simple protocol also keeps the app
working behind a transaction-pooling pgbouncer. Arguments are still escaped by
pgx, so this is not a SQL-injection trade.

## Consequences
+ The database can be backed up, monitored, and restored by whatever already
  does that for other services; the app container becomes stateless.
+ Removes SQLite's single-writer ceiling before the schema is written against it.
+ `make dev` and `go test ./...` still need nothing but a Go toolchain.
+ Testing both engines on every CI run means portability is verified, not hoped
  for.
- Every query from Phase 1 onward must stay in the portable subset, and every
  migration must be written twice. This is real, ongoing discipline — the cost
  the ORM would have removed.
- Two engines mean two sets of behaviour to know: type affinity, `RETURNING`
  support, transaction isolation, and date/decimal handling all differ. Monetary
  amounts in particular must not rely on either engine's numeric defaults —
  Phase 1 should store amounts as integer cents, which is unambiguous in both.
- The deployment story is no longer "one binary and a file." A Postgres
  deployment has a second component to run and back up. SQLite mode remains for
  anyone who wants the original story.
- ADR 0001's SQLite rationale still stands for what it now covers (dev, test,
  single-user); only its "SQLite is the database" claim is superseded.

## Alternatives considered
- **Stay SQLite-only, put the file on network storage:** rejected — SQLite over
  NFS/SMB has known locking failure modes, and the result is worse than either
  clean option.
- **Switch to Postgres exclusively, drop SQLite:** rejected — it would put a
  database server between a fresh checkout and a running app, and make the test
  suite depend on a container.
- **An ORM or `sqlc` to abstract the dialects:** rejected for now — a
  substantial dependency plus a code-generation step, against ADR 0001's "no
  build step" constraint. Worth revisiting only if hand-maintained portability
  actually starts producing bugs.
- **MySQL/MariaDB as a third engine:** not requested; each additional dialect
  multiplies the migration and portability burden.
