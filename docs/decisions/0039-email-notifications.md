# 0039 — Email notifications to the owner

## Context
The reminders (recurring invoices, overdue invoices, backups, waiting
lists) only showed when the app was open. The user wanted them on the
phone without a third-party notification service, declined a calendar feed,
and asked for mail via SMTP. ADR 0038 rules out automatic mail to
customers.

## Decision
- **Only to the owner**: one address, set under Einstellungen →
  Benachrichtigungen. The app never mails a customer.
- **SMTP with the standard library** (`internal/notify`): STARTTLS on 587,
  TLS from the start on 465, unencrypted only to a relay on the same
  machine. A server without STARTTLS is refused rather than logged in to in
  clear text. Plain-text mail, UTF-8, quoted-printable, marked
  `Auto-Submitted`.
- **The mail server is configured from the environment**
  (`BOOKKEEPER_SMTP_*`, the password also from a file), like the login
  password (ADR 0017), so no credential is in the database, a backup or an
  archive. The settings hold the recipient and the kinds only.
- **Events** with a key each, checked hourly: a recurring invoice due within
  a week and when due, an invoice going overdue, no backup or one older than
  a week (once a week), a failed automatic backup (at once, once a day), a
  waiting list older than a day. New events go out together in **one mail
  per run**; mailed keys are kept in `notifications_sent` (left out of
  backups), so each reminder comes once.
- Mails follow the UI language and date format and link into the app when
  `BOOKKEEPER_PUBLIC_URL` is set. A test mail on the settings page checks the
  server before the first reminder depends on it.

## Consequences
- The mail goes through the user's mail provider, which the user accepted.
- After a restore, earlier reminders can come once more, since the record
  of sent mails is not in the backup.
- Reminders need the app running; a laptop asleep sends nothing.

## Alternatives considered
- **Web Push**: needs HTTPS and runs over Apple's or Google's push service.
- **A calendar feed**: no third party, declined by the user.
- **A self-hosted ntfy or Gotify**: free of third parties on Android only,
  and one more service to run.
- **SMTP credentials in the settings**: they would end up in every backup.
