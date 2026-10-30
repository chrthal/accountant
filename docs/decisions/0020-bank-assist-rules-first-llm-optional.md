# ADR 0020: Bank-line assistance: rules first, an optional local LLM for the rest

**Date:** 2026-10-15
**Status:** Accepted. Extends ADR 0009 and ADR 0014.

## Context
The question was whether a lightweight LLM could format the CSV import,
create expenses from bank lines, and match incoming payments. Most bank lines
of a small business repeat: rent, phone, social insurance (SVS), the same
customers. The ledger is append-only, so a wrong booking costs a Storno, and
whatever the app proposes the user must be able to trust or see through
(CLAUDE.md: no plausible-looking guesses).

## Decision

### Rules do the work wherever a rule can
1. **Labelled bank text is split** (`bankcsv.SplitText`). meinELBA has no
   counterparty column, only "Empfänger: … IBAN Empfänger: … Verwendungszweck:
   …" in one field. The known labels are parsed. Text without them (a card
   payment) stays as it was. The bank's original text is kept in
   `bank_transactions.raw_text`, and the dedup hash is unchanged. Rows
   imported earlier are split once at startup.
2. **Incoming payments are matched to invoices** (`internal/match`): invoice
   number in the transfer text ("RE 2026-0001", "2026/0001", "2026 0001"),
   exact open amount, and sender name. Names are compared after folding
   umlauts and dropping legal forms, so "ALPENBLICK HOTEL GES.M.B.H." matches
   "Alpenblick Hotel GmbH". Every hint states its reasons. A number with the
   wrong amount is flagged ("Betrag weicht ab") and never booked, because
   partial payments are not supported (ADR 0015). **"Alle eindeutigen
   zuordnen"** books only transfers that name exactly one invoice and pay
   exactly its open amount, where no other transfer claims that invoice.
3. **Recurring payees reuse the last booking** (`Store.PreviousBooking`): the
   same IBAN, or failing that the same name, in the same direction. The draft
   takes over that booking's description, category, contact and VAT rate
   ("wie zuletzt"). The IBAN keeps this working when the bank renames the
   payee ("Hausverwaltung Huber GmbH" → "HV Huber").

### An optional local LLM fills only what the rules leave empty
- `BOOKKEEPER_LLM_URL` (an Ollama server) turns it on, and
  `BOOKKEEPER_LLM_MODEL` defaults to `qwen2.5:3b` (about 2 GB, CPU, 1–2 s per
  line). Off by default, like OCR. In compose it is an opt-in profile
  (`make up-llm`, `make llm-pull`). The model runs beside the app, so bank
  data stays on the machine (ADR 0002).
- It is only asked about open lines with no invoice to pay, no earlier
  booking and no stored suggestion. A background worker runs after each
  import or on "KI-Vorschläge holen". Answers are cached in `bank_suggestions`,
  which is derived data and left out of backups on purpose (ADR 0019,
  `derivedTables`).
- The answer is constrained by a JSON schema at temperature 0 and then
  **validated against real data**: only existing categories of the right
  direction, only VAT rates 0/10/13/20, and **only a contact whose name matches
  the counterparty or appears in the bank text**. Bank text goes into the prompt
  as data. The worst a crafted transfer text can achieve is a wrong suggestion
  that is shown and not booked.

### Nothing is booked without the user
`/bank/drafts` lists the open lines with their proposal and its source
("wie zuletzt", or "KI-Vorschlag" plus "unsicher" when the model said so).
Each row can be edited and booked alone, or the ticked rows together. Only
"wie zuletzt" and confident model suggestions are preselected. **The VAT rate
is marked "prüfen" unless it comes from an earlier booking**, because a bank
line never states it. Under the Kleinunternehmerregelung every draft is at 0 %.

## What testing against the real model changed
The drill ran on real bank lines with `qwen2.5:3b`:
- It picked the right category every time (rent, phone, groceries).
- It **filed the landlord and a supermarket payment under the only customer
  contact** it was shown. Hence the grounding rule for contacts. It costs one
  correct semantic match (Magenta for T-Mobile), which is the right trade.
- It **said "low confidence" while being exactly right**. Self-reported
  confidence is therefore only displayed and never used to drop fields.

## Consequences
- The rules work with no model at all, and the model is a fallback for first-time
  payees.
- A second service (Ollama) when enabled: 2–3 GB RAM while answering, and a
  one-time model download.
- Migrations 0009 (`counterparty_iban`, `raw_text`) and 0010
  (`bank_suggestions`).

## Alternatives considered
- **LLM for everything, including CSV formatting and invoice matching:** for
  these, exact rules are more reliable, faster and explainable, and the column
  guess (ADR 0014) already covers the format.
- **Auto-booking confident suggestions:** an append-only ledger turns every
  wrong guess into a Storno, and VAT rates are a tax decision.
- **A cloud model:** bank data would leave the machine (ADR 0002).
- **Embedding the model in the app image:** it would add gigabytes to an
  80 MB image for a feature many users will not enable.
