# 0040 — Mail server set up in the web UI

## Context
ADR 0039 took the mail server for notifications from environment variables
only, to keep its password out of the database and the backups. The user
wanted to set it up in the web UI and send a test mail from there.

## Decision
- The settings page has the mail server fields: host, encryption
  (STARTTLS, TLS, none only for a server on this machine), port (empty
  picks 587, 465 or 25 to match), user, password, sender.
- They are stored in their own table, `mail_server`, **left out of backups
  and the year archive** (like `notifications_sent`): no credential leaves
  the database. After a restore the server is entered again.
- The password is **write-only**: never sent to the page; an empty field
  keeps the stored one. It is stored as typed. Encrypting it would need a
  key, and a key next to the database protects nothing the database's own
  protection does not.
- The mail server is checked before anything is saved, so an invalid one
  leaves the other settings unchanged.
- **"Test-Mail senden"** uses what is typed in the form, saved or not, with
  the stored password when the field is empty.
- **Environment variables still win** (ADR 0039): when `BOOKKEEPER_SMTP_HOST`
  is set, the page shows that server read-only.
- The notifier runs always and finds the mail server on every run, so a
  server set in the UI applies without a restart.

## Consequences
- Whoever can read the database can read the mail password. The database
  holds the books themselves, so it needs that protection anyway.
- Anyone who can use the settings page can make the app connect to a mail
  server of their choice. That is the single user or small team the app is
  built for (ADR 0017).

## Alternatives considered
- **Environment only** (ADR 0039 as it was): declined by the user.
- **Storing the password in the settings table**: it would be in every
  backup.
