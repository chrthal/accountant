# kmu-buchhaltung

Bookkeeping for a small Austrian business (EPU/KMU): income & outgoings on an
append-only ledger, invoices with per-line VAT and Kleinunternehmer mode, bank
CSV import with matching, a Kleinunternehmer threshold tracker, CSV and DATEV
exports, and receipt OCR. A single static Go binary, no JS build step,
installable on a phone as a PWA.

Scope and phases: [`plans/0001-mvp-scope.md`](plans/0001-mvp-scope.md) (MVP) and
[`plans/0002-post-mvp.md`](plans/0002-post-mvp.md) (redesign, invoice payments,
reconciliation, Belege, VAT periods, security).
Why things are the way they are: [`docs/decisions/`](docs/decisions/README.md).

## Database

One setting picks the engine (ADR 0006) — `BOOKKEEPER_DSN`, or the `-dsn` flag,
which wins over the env var:

```sh
BOOKKEEPER_DSN=postgres://user:pass@host:5432/bookkeeper?sslmode=disable  # deployments
BOOKKEEPER_DSN=sqlite://bookkeeper.db                                    # dev, tests
```

PostgreSQL is what a real deployment runs. SQLite keeps `make dev` and the test
suite to a single binary with no server, and is still a fine way to run the app
for one user. Anything else is refused at startup.

Writing queries: stay in the portable subset, use `?` placeholders and pass the
query through `Store.Rebind` (it rewrites to `$1, $2, …` on Postgres), and add
every migration **twice** — `internal/store/migrations/postgres/` and
`.../sqlite/`, same tables and columns, only syntax differing. Store money as
integer cents; do not rely on either engine's numeric defaults.

## Login and security

Set a password and the whole app sits behind a login (ADR 0017). There are no
user accounts: it is one set of books, and whoever keeps them shares the
password.

```sh
BOOKKEEPER_PASSWORD='…'                          # or:
BOOKKEEPER_PASSWORD_FILE=/run/secrets/bookkeeper # container secret, kept out of `docker inspect`
```

Without a password the app is open, which is fine on `localhost` or behind a
proxy that authenticates. When it listens on a network address without a
password, it logs a warning at startup. Regardless of the login, cross-site form
posts are refused, and every page carries a strict Content-Security-Policy (no
inline script or style). Terminate TLS in front of it and forward
`X-Forwarded-Proto: https` so the session cookie is marked `Secure`.

## Bank assistance (rules, plus an optional local model)

After an import, `/bank` and `/bank/drafts` do most of the matching work
(ADR 0020):

- **meinELBA-style text** ("Empfänger: … IBAN … Verwendungszweck: …") is
  split into counterparty, IBAN and purpose. The original text is kept.
- **Customer payments** are matched by invoice number in the text, exact
  amount and sender name. "Alle eindeutigen zuordnen" books the unambiguous
  ones.
- **Recurring payees** (rent, phone, SVS) are proposed "wie zuletzt", with the
  last booking's category, contact and VAT rate.
- **Categories are assigned by keyword** (set them on each category, e.g.
  "billa, spar") or by the category's name in the bank text (ADR 0021).
  Expenses get the category's default VAT rate, or the standard rate from
  Einstellungen. The model never sets the VAT rate of an expense.
- **Everything else** can get a suggestion from a local model. Nothing is
  booked until you confirm it in `/bank/drafts`.

The model is optional and runs on your machine:

```sh
BOOKKEEPER_LLM_URL=http://127.0.0.1:11434 make dev   # an Ollama you already run
make up-llm && make llm-pull                          # or the compose profile (~2 GB model)
```

`BOOKKEEPER_LLM_MODEL` defaults to `qwen2.5:3b`. Its answers are checked
against your categories, contacts and the valid VAT rates before they are
shown. A bank line never states the VAT rate, so drafts mark it "prüfen".

## Backups

A backup is one file holding all books, invoices, bank transactions, settings
**and all Belege** (ADR 0019). It is checksummed per table, and it restores into
either engine.

```sh
make backup                           # dev database → ./backups, verified
make backup-compose                   # the compose app's database → ./backups
bookkeeper backup -o /some/dir        # any database (BOOKKEEPER_DSN / -dsn)
bookkeeper verify  FILE               # check a file without restoring it
bookkeeper restore -dsn DSN FILE      # into an EMPTY database only
```

**Automatic daily backups:** set `BOOKKEEPER_BACKUP_DIR` (and optionally
`BOOKKEEPER_BACKUP_KEEP`, default 30). `compose.yaml` turns this on, into a
`bookkeeper-backups` volume of its own. The overview warns when the last backup
is more than a week old. Einstellungen offers a download.

**Restore** goes into an empty database: a new SQLite file, or a newly
created Postgres database. It refuses anything else, migrates the target to the
backup's schema, loads the rows, verifies them against the file's checksums
before committing, and then migrates to the current schema. So backups taken
by older versions still restore.

**Restore in the browser:** a new installation (nothing in it but the default
categories) shows an upload form under Einstellungen → Datensicherung, and the
overview links to it. The file is checked completely first, then loaded and
compared with its checksums in one transaction (ADR 0030). It works for
backups from v0.1.0 on; older ones go through `bookkeeper restore`.

**Year archive:** Auswertung → "Archiv mit Belegen (ZIP)" is the year as plain
files: bookings CSV, every Beleg, every invoice PDF (ADR 0029). It needs no
software of ours to read, but it does not restore the app.

Copy backups off the host. A backup on the same disk does not survive the
disk.

## Email reminders (optional)

The app can mail reminders to **one address of yours**, never to customers
(ADR 0039): recurring invoices coming due, invoices going overdue, a backup
that is old or failed, receipts or imported invoices waiting on a review
list. Each event is mailed once; several go out together. The recipient and
the kinds are chosen under Einstellungen → Benachrichtigungen, which also
has a test mail. The mail server is set up there too (ADR 0040): the password
is write-only and kept out of backups and archives. Or set it here, which
wins over the settings page and keeps the password out of the database:

```sh
BOOKKEEPER_SMTP_HOST=mail.example.at
BOOKKEEPER_SMTP_PORT=587                 # 465 switches to TLS from the start
BOOKKEEPER_SMTP_TLS=starttls             # starttls | tls | none (none: only a relay on localhost)
BOOKKEEPER_SMTP_USER=buchhaltung@example.at
BOOKKEEPER_SMTP_PASSWORD_FILE=/run/secrets/smtp   # or BOOKKEEPER_SMTP_PASSWORD
BOOKKEEPER_SMTP_FROM=buchhaltung@example.at       # defaults to the user
BOOKKEEPER_PUBLIC_URL=https://buchhaltung.example.at   # optional, for links in the mail
```

Login is only ever sent over an encrypted connection; a server without
STARTTLS is refused.

## Receipt OCR and PDF invoices (optional)

Photographing a receipt or uploading a supplier's PDF invoice pre-fills a
booking (amount, date, vendor, VAT rate, the supplier's invoice number), and
the file is kept as the booking's Beleg. Everything runs locally, with no
cloud service:

- photos are read by `tesseract` (ADR 0011);
- PDFs are read from their text layer by `pdftotext`, and scanned PDFs are
  rendered by `pdftoppm` and then go through tesseract (ADR 0018).

All of it is optional. Without the tools the upload page says what is
missing, the file is still stored as the Beleg, and the figures are typed by
hand.

```sh
brew install tesseract tesseract-lang poppler                  # macOS
apk add tesseract-ocr tesseract-ocr-data-deu tesseract-ocr-data-eng poppler-utils  # already in the container image
```

The languages actually installed are detected at runtime, because asking
tesseract for a missing one makes it fail rather than degrade.

## Develop

```sh
make dev            # SQLite, no container, http://localhost:8080 (PORT=… to move it)
make dev-postgres   # same, against a throwaway Postgres container
make test           # vet + tests (SQLite; Postgres cases skip)
make test-postgres  # both engines, like CI
make help           # all targets
```

## Containers

The Dockerfile and `compose.yaml` avoid BuildKit-only syntax, bind mounts, and
Docker-only compose options, so both runtimes work. `make` picks up whichever of
`docker` / `podman` is on PATH; override with `make CONTAINER=podman …`.

```sh
make up              # app + Postgres, the production-shaped path
make up-sqlite       # app alone on SQLite, no database server
make logs
make down            # stop; named volumes (and your data) survive
make test-container  # vet + test inside the builder image — same as CI
make clean           # stop and DELETE the database volumes (backups are kept)
```

### Podman on macOS

Verified with podman 6.1 on an `applehv` machine (2026-10-15): `make
test-postgres`, `make up`, `make up-sqlite` and `make test-container` all run
unchanged. One-time setup:

```sh
brew install podman docker-compose   # docker-compose is podman compose's provider
podman machine init --cpus 4 --memory 4096
podman machine start
```

With Docker Desktop not installed, `make` finds `podman` on its own. With both
installed, pass `CONTAINER=podman`. A shell alias `docker=podman` does not
count, because `make` does not see aliases.

`make dev` and `make up` both default to port 8080. To run them side by side,
move one of them: `make up PORT=8081`.

`podman compose` delegates to `podman-compose` or `docker-compose`. If neither
is installed, skip compose entirely:

```sh
make run-image CONTAINER=podman
```

The container is stateless: state lives in Postgres, or in a SQLite file on the
`/data` volume. Set `TZ` (default `Europe/Vienna`) — booking dates must not
shift under UTC.

## Release

First give the version its section in [`CHANGELOG.md`](CHANGELOG.md): move
what is under `[Unreleased]` to `## [X.Y.Z] - YYYY-MM-DD` and commit. A
`vX.Y.Z` tag then triggers [`.github/workflows/release.yaml`](.github/workflows/release.yaml):
test → release notes from that changelog section (no section, no release) →
build → verify the version is stamped into the binary → push
`ghcr.io/chrthal/accountant:<version>` and `:latest` → open a GitHub
release with the notes. `linux/amd64` only (ADR 0005). Pushes to `main` publish a floating
`:edge` tag.

```sh
git tag v0.1.0 && git push origin v0.1.0
docker pull ghcr.io/chrthal/accountant:0.1.0
docker run --rm -p 8080:8080 \
  -e BOOKKEEPER_DSN='postgres://user:pass@db:5432/bookkeeper?sslmode=disable' \
  ghcr.io/chrthal/accountant:0.1.0
```

With no `BOOKKEEPER_DSN` set the image falls back to `sqlite:///data/bookkeeper.db`,
so `docker run -v bookkeeper-data:/data …` works with nothing else running.

The Dockerfile does not pin `GOARCH`: the binary is built for the build
platform, matching the runtime base image (so an image built on an Apple Silicon
Mac is arm64 and runs there). The amd64-only release comes from the CI runner
being amd64, which `release.yaml` asserts before pushing.

CI runs on GitHub's `ubuntu-latest` runners. Postgres runs as a service
container, and the OCR tests run inside a container that has tesseract and
poppler, so they do not depend on what the runner image ships. The workflows
push to the GitHub Container Registry with the built-in `GITHUB_TOKEN`; no
secrets need to be set up.

Running it in production is up to you: the image needs a Postgres database
(or a volume for the SQLite file) and a reverse proxy for HTTPS in front.

## License

[MIT](LICENSE)
