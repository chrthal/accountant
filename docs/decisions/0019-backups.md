# ADR 0019: Backups as a verified logical export, restorable into either engine

**Date:** 2026-10-15
**Status:** Accepted

## Context
ADR 0016 put the Belege into the database on the grounds that "a database
backup is a backup of the books and their evidence". Nothing took that
backup. § 132 BAO requires Belege and records to be kept for seven years, so
one lost disk or one bad migration away from the books being gone is a legal
problem, not just an inconvenience.

Requirements:
- both engines (Postgres in deployments, SQLite in dev and small setups);
- runs inside the container without extra tools;
- a restore that **proves** it is complete, not one that merely runs;
- an old backup must still restore after the app has been upgraded;
- someone notices when backups stop.

## Decision

### A logical export, one format for both engines
`bookkeeper backup` writes one gzipped JSON-lines file: a header (format
version, app version, engine, applied migrations), then each table's columns
and rows, each table closed by its row count and a SHA-256 over its row lines,
and a trailer. Every value is typed by its column (int, text, time, bytes).
Timestamps are normalised to UTC microseconds, so the same books produce the
same hash on either engine.

The export reads in one read-only transaction (`REPEATABLE READ` on Postgres),
so a backup taken while the app is in use is still a consistent snapshot.

### The table list is checked against the live schema
`backupTables` lists every table and column. `CheckBackupCoverage` compares it
with the live schema and runs first in every backup, and a test runs it on
both engines. A migration that adds a table or column without extending the
list therefore breaks the test, and in production stops backups with a
clear error. Backups can never silently miss a table. `app_meta` and
`schema_migrations` are left out on purpose, because they belong to the
migrator.

### Restore: empty target, own schema first, verified before commit
`bookkeeper restore` only accepts an **empty** database. Merging a backup into
existing books would create exactly the duplicates and gaps a backup exists
to prevent. Then:
1. the target is migrated only as far as the backup's last migration;
2. all rows go in, in one transaction, in foreign-key order;
3. the restored rows are re-read and hashed, and **any difference to the file
   rolls everything back**;
4. Postgres identity sequences are moved past the restored ids;
5. the remaining migrations run.

Step 1 plus step 5 is what makes old backups restorable. A backup from before
0007 still has its bank match in `matched_entry_id`, and restoring it runs
0007 over it, exactly as an upgrade did on the live database. A test covers
this case.

Restore is command-line only. A browser button that replaces the books is a
mistake waiting to happen.

### Three ways to take one, one that runs by itself
- `bookkeeper backup -o DIR|FILE|-` (and `make backup`, `make backup-compose`)
- "Sicherung herunterladen" under Einstellungen (`GET /backup`, behind the
  login)
- **daily, automatically**, when `BOOKKEEPER_BACKUP_DIR` is set, keeping the
  newest `BOOKKEEPER_BACKUP_KEEP` (default 30). It is on in `compose.yaml`,
  with its own volume, so that deleting the data volume does not take the
  backups with it. The scheduler checks hourly and decides from the newest file
  in *its own* directory. A download elsewhere does not stand in for it, and
  an emptied directory refills.

Files are written to a temp file, read back and verified, then renamed into
place with mode 0600, so a file with a backup name is always complete. A
streamed backup (download, `-o -`) cannot be checked before it leaves. If it
fails midway, the file lacks its trailer, and verify and restore reject it.

### Someone notices
Every completed backup records its time. The overview's "Zu erledigen" shows
"Datensicherung", in red when there was none, or none for more than 7 days.

## Consequences
- A SQLite backup restores into Postgres and back. This is also the supported
  way to move from the dev engine to a real deployment.
- Backups on the same host do not survive the host. Copying them off (NAS,
  another machine, object storage) is the operator's job. The README and the
  settings page say so, and nothing here pretends otherwise.
- Backups are not encrypted. They are as sensitive as the database and are
  written 0600. Encrypting them for off-site storage (e.g. `age`) is a natural
  follow-up once the deploy target is known.
- `make clean` no longer uses `down -v`, which would delete the backups volume
  together with the books it is meant to protect.

## Alternatives considered
- **`pg_dump` / copying the SQLite file:** two formats, two restore
  procedures, no cross-engine restore. `pg_dump` must match the server's major
  version, and the container would need the Postgres client tools. It is
  still a fine *additional* layer for whoever runs the Postgres server.
- **SQLite `VACUUM INTO`:** consistent and simple, but SQLite only.
- **Restore from the browser:** convenient, and one wrong click away from
  replacing the books.
- **Encrypting by default:** needs key management before there is a deploy
  target to manage keys for.
