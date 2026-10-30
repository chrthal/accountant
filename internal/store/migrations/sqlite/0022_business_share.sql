-- ADR 0041: the business share of an expense used also privately, e.g. a
-- phone or internet contract. Keep in lockstep with
-- postgres/0022_business_share.sql.
--
-- It is stored on the booking, not on the category: a booking must not
-- change after a VAT return was filed (ADR 0007). It lowers the cost for
-- the profit and the deductible Vorsteuer. business_note says how the share
-- was found, for the tax office.

ALTER TABLE entries ADD COLUMN business_pct INTEGER NOT NULL DEFAULT 100
    CHECK (business_pct BETWEEN 1 AND 100);
ALTER TABLE entries ADD COLUMN business_note TEXT NOT NULL DEFAULT '';
