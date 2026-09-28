-- The scheduled export remembers the admin's last choice of layout, the same
-- as the manual "Групирай по обекти" checkbox on the activations page.
ALTER TABLE export_schedule ADD COLUMN group_by_client INTEGER NOT NULL DEFAULT 0;
