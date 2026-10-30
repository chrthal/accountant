# 0030 — Restore in the browser, into a fresh installation

## Context
Backups (ADR 0019) restored only through `bookkeeper restore` on the
server, into a database created by hand. For a self-hosted tool used by
people who are not administrators, a backup that cannot be brought back
without a shell is not enough on its own. The user asked for the restore
in the browser.

Two things stood in the way. A running server has already migrated its
database to the current schema, so the CLI's "migrate to the backup's
schema, load, migrate on" cannot run in place. And since ADR 0027, a new
installation is not empty: it holds the default categories.

## Decision
- **Only into a fresh installation**: every table that backups cover is
  empty, except categories that are exactly the defaults. Restoring over
  existing books stays impossible, in the browser as on the CLI. Freshness
  is checked again inside the restore transaction.
- **Rows go straight into the current schema.** That is correct as long as
  no migration after the backup's version changes existing rows: the others
  only add tables and columns with defaults. Migrations that write rows are
  listed in `dataMigrations` (today 0005 and 0007, both before v0.1.0); a
  backup from before one of them is refused with a pointer to the CLI. A
  test fails when a migration writes rows and is not listed.
- **Checked before anything changes**: the upload is streamed to a
  temporary file, verified completely (structure, counts, checksums), then
  loaded in one transaction that deletes the default categories, inserts
  the rows, reads them back, compares them with the file and resets the
  Postgres id sequences. Any difference rolls everything back. The CLI
  restore uses the same loading code.
- A fresh installation shows the upload form under Einstellungen →
  Datensicherung, and the overview's welcome notice links to it. Elsewhere
  the CLI instructions remain.
- Upload limit 4 GiB; the file never sits in memory as a whole.

## Consequences
- Checked against the real image: a backup of a Postgres installation went
  into a fresh SQLite container through the form, and a backup taken from
  the result had identical table checksums.
- Anyone who can reach a fresh installation can fill it from a backup. That
  is no more than they could do by typing, and a set `BOOKKEEPER_PASSWORD`
  (ADR 0017) applies to the restore as to every page.
- A new data migration needs a decision: list it (older backups then need
  the CLI) or write it so that it does not touch rows.

## Alternatives considered
- **Drop the schema and run the CLI restore in place**: would have to know
  every object the migrations create, and on a shared Postgres schema one
  mistake deletes someone else's tables.
- **Restore over existing books after a confirmation**: a backup merged into
  or replacing live books is the failure a backup exists to prevent.
