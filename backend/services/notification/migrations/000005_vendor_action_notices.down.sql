-- Refuses while shop notices or preferences exist: they record who was
-- told about which work. Clear them deliberately first if that is intended.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM vendor_action_events) OR EXISTS (SELECT 1 FROM notification_preferences)
        OR EXISTS (SELECT 1 FROM notification_admin_audit WHERE entity_type <> 'notification') THEN
        RAISE EXCEPTION 'vendor action notices exist; refusing to drop them';
    END IF;
END $$;
ALTER TABLE notification_admin_audit DROP CONSTRAINT IF EXISTS notification_admin_audit_entity_type_check;
ALTER TABLE notification_admin_audit ADD CONSTRAINT notification_admin_audit_entity_type_check
    CHECK (entity_type IN ('notification'));
DROP TABLE notification_preferences;
DROP TABLE vendor_action_events;
