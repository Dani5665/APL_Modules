-- The app is standalone: nothing links into it from a parent application any
-- more, so there are no parked entry links.
DROP TABLE IF EXISTS pending_entry_links;
UPDATE users SET must_change_password = 0;
