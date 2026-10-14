-- A store is now identified by its MANDANT_ID in the external database, with
-- MANDANT_NAME as its single name (the former separate "external value" is
-- gone). Existing stores carry no MANDANT_ID, so they and their assignments
-- are removed; stores are re-created in the admin panel with their ids.
DELETE FROM user_stores;
DELETE FROM stores;

ALTER TABLE stores DROP COLUMN external_value;
ALTER TABLE stores ADD COLUMN mandant_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX idx_stores_mandant_id ON stores(mandant_id);
