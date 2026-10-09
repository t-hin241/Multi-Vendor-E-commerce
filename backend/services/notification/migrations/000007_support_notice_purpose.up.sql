-- PW-009: shop notices about support cases (purpose "support", permission
-- support.reply); staff may opt into the category.
ALTER TABLE vendor_action_events DROP CONSTRAINT IF EXISTS vendor_action_events_purpose_check;
ALTER TABLE vendor_action_events ADD CONSTRAINT vendor_action_events_purpose_check
    CHECK (purpose IN ('orders', 'returns', 'finance', 'support'));
ALTER TABLE notification_preferences DROP CONSTRAINT IF EXISTS notification_preferences_vendor_categories_check;
ALTER TABLE notification_preferences ADD CONSTRAINT notification_preferences_vendor_categories_check
    CHECK (vendor_categories <@ ARRAY['orders', 'returns', 'finance', 'support']::text[]);
