-- ADR 0020: counterparty split out of labelled bank text. Keep in lockstep
-- with sqlite/0009_bank_text.sql.
--
-- counterparty_iban comes from "IBAN Empfänger:" / "IBAN Auftraggeber:" and is
-- what counterparty defaults key on. raw_text keeps the bank's original text
-- whenever the app split it, so the split never loses what the bank sent.

ALTER TABLE bank_transactions ADD COLUMN counterparty_iban TEXT NOT NULL DEFAULT '';
ALTER TABLE bank_transactions ADD COLUMN raw_text          TEXT NOT NULL DEFAULT '';
