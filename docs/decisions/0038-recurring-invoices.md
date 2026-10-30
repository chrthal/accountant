# 0038 — Recurring invoices: reminders, no automatic sending

## Context
Some invoices are the same every month or year: hosting, maintenance, a
retainer. "Als Vorlage" (copy an invoice) needed the user to remember when.
The user asked for recurring invoices with a reminder, shown on the
customer, and explicitly without automatic email.

## Decision
- A **recurring invoice** is a template (`recurring_invoices`): customer,
  name, rhythm (`frequency`: monthly, quarterly, yearly), the date the next
  invoice is due, positions as typed (JSON, like a draft, ADR 0026), a note
  and active/paused. Positions are checked like an invoice's on save.
- **Reminders, nothing more**: from a week before the date, the overview and
  the invoice list show it as due ("fällig" once the date has passed). Pressing
  "Rechnung erstellen" opens the ordinary invoice form, filled: customer,
  positions, note, the due date as service date, and `{Zeitraum}` or
  `{Period}` replaced by "10/2026", "Q4 2026" or "2026". The app neither
  issues nor sends anything by itself.
- **Issuing moves it on**: an invoice issued from the form, also via a draft,
  carries `recurring_id`; `CreateInvoice` moves the template's date by one
  interval in the same transaction. A month end stays a month end (31 Jan,
  28 Feb, 31 Mar); a later day is clamped to the month's last.
- **Skip** moves the date without an invoice; **pause** stops reminders;
  **delete** only while no invoice was issued from it (the invoices keep the
  reference), otherwise pause.
- **On the customer**: the contact page lists its recurring invoices with
  the last invoice, and starts a new one. An issued invoice offers "Als
  wiederkehrend anlegen". A contact with a recurring invoice cannot be
  deleted.
- Templates and the reference are in backups.

## Consequences
- Issuing is still the user's act, with the invoice's checks and its
  numbering; a template cannot issue a wrong invoice on its own.
- A template keeps the VAT rates typed; while the Kleinunternehmerregelung
  applies, the form fills 0 %.
- Invoicing in advance or in arrears is the user's choice of date: the due
  date is also the invoice's service date.

## Alternatives considered
- **Automatic issuing and email**: declined by the user; it would also
  issue invoices nobody checked.
- **A reminder only, without a template**: the form would still have to be
  filled by hand every time.
