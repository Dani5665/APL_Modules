-- A request may ask for an activation without an end date. months stays 1..12
-- (its CHECK is untouched); unlimited says the month count is to be ignored.
ALTER TABLE requests ADD COLUMN unlimited INTEGER NOT NULL DEFAULT 0;
