-- Category keywords and default VAT rate (ADR 0021). Keep in lockstep with
-- postgres/0011_category_defaults.sql.
--
-- keywords: comma-separated words that assign a bank line to this category
-- ("billa, spar, hofer"). default_vat_rate: the rate a booking in this
-- category usually carries, NULL when the category has none.

ALTER TABLE categories ADD COLUMN keywords         TEXT NOT NULL DEFAULT '';
ALTER TABLE categories ADD COLUMN default_vat_rate INTEGER CHECK (default_vat_rate IN (0, 10, 13, 20));
