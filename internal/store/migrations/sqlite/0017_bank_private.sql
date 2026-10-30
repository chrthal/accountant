-- ADR 0035: private transfers. Keep in lockstep with
-- postgres/0017_bank_private.sql.
--
-- A bank line between the business account and the owner (Privatentnahme,
-- Privateinlage) is not income or expense in the Einnahmen-Ausgaben-Rechnung.
-- It is marked private instead of matched to a booking; NULL means it is not.

ALTER TABLE bank_transactions ADD COLUMN private_at TIMESTAMP;
