-- Each email template that is not sent back to a request's submitter carries
-- its own list of recipients. The lists that used to live elsewhere move here:
-- the notification addresses (new requests) and the export schedule's
-- addresses. The old columns and keys stay in place, unused.
ALTER TABLE email_templates ADD COLUMN recipients TEXT NOT NULL DEFAULT '';

UPDATE email_templates
   SET recipients = COALESCE((SELECT value FROM settings WHERE key = 'notify.recipients'), '')
 WHERE key = 'request_created';

UPDATE email_templates
   SET recipients = COALESCE((SELECT recipients FROM export_schedule WHERE id = 1), '')
 WHERE key = 'scheduled_export';
