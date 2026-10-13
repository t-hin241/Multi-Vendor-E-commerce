-- Refuses rollback when Inventory history/preferences exist; roll back by
-- disabling the producer, draining existing notices and retaining history.
ALTER TABLE vendor_action_events DROP CONSTRAINT vendor_action_events_source_check;
ALTER TABLE vendor_action_events ADD CONSTRAINT vendor_action_events_source_check CHECK(source IN ('order','payment'));
ALTER TABLE vendor_action_events DROP CONSTRAINT vendor_action_events_purpose_check;
ALTER TABLE vendor_action_events ADD CONSTRAINT vendor_action_events_purpose_check CHECK(purpose IN ('orders','returns','finance','support'));
ALTER TABLE notification_preferences DROP CONSTRAINT notification_preferences_vendor_categories_check;
ALTER TABLE notification_preferences ADD CONSTRAINT notification_preferences_vendor_categories_check CHECK(vendor_categories <@ ARRAY['orders','returns','finance','support']::text[]);
