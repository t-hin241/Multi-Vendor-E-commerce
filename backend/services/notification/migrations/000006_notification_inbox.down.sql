-- Refuses once a consent was recorded (audit is never deleted). Inbox
-- items are a read model and are dropped with the table.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM notification_consent_audit) OR EXISTS (SELECT 1 FROM notification_preferences WHERE marketing_opt_in) THEN
        RAISE EXCEPTION 'marketing consent was recorded; refusing to drop it';
    END IF;
END $$;
DROP TRIGGER notification_consent_audit_append_only ON notification_consent_audit;
DROP FUNCTION notification_consent_audit_append_only();
DROP TABLE notification_consent_audit;
ALTER TABLE notification_preferences
    DROP COLUMN marketing_withdrawn_at, DROP COLUMN marketing_consented_at, DROP COLUMN marketing_opt_in;
DROP TABLE inbox_items;
