-- A template can be set to always copy (CC) the user who filed the request.
-- The outbox keeps the CC addresses of each queued message.
ALTER TABLE email_templates ADD COLUMN cc_submitter INTEGER NOT NULL DEFAULT 0;
ALTER TABLE email_outbox ADD COLUMN cc TEXT NOT NULL DEFAULT '';
