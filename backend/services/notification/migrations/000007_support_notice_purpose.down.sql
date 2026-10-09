-- Refuses once a support notice or opt-in exists: the old checks cannot hold them.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM vendor_action_events WHERE purpose = 'support')
        OR EXISTS (SELECT 1 FROM notification_preferences WHERE 'support' = ANY (vendor_categories)) THEN
        RAISE EXCEPTION 'support notices or opt-ins exist; refusing to narrow the checks';
    END IF;
END $$;
ALTER TABLE vendor_action_events DROP CONSTRAINT IF EXISTS vendor_action_events_purpose_check;
ALTER TABLE vendor_action_events ADD CONSTRAINT vendor_action_events_purpose_check
    CHECK (purpose IN ('orders', 'returns', 'finance'));
ALTER TABLE notification_preferences DROP CONSTRAINT IF EXISTS notification_preferences_vendor_categories_check;
ALTER TABLE notification_preferences ADD CONSTRAINT notification_preferences_vendor_categories_check
    CHECK (vendor_categories <@ ARRAY['orders', 'returns', 'finance']::text[]);
