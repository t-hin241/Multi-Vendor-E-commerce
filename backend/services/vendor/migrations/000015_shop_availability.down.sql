-- Refuses while a shop is paused or has vacation history: dropping it would
-- reopen paused shops behind their owners' backs. Roll back by turning
-- FEATURE_SHOP_VACATION_ENABLED off (no new schedule; open ones still end).
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM vendors WHERE NOT accepting_orders)
    OR EXISTS (SELECT 1 FROM vendor_availability_intervals) THEN
  RAISE EXCEPTION 'shop vacation records exist; keep migration 000015 and disable FEATURE_SHOP_VACATION_ENABLED';
 END IF;
END $$;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM vendor_notification_outbox WHERE type IN ('shop_vacation_started', 'shop_vacation_ended')) THEN
  RAISE EXCEPTION 'shop vacation notices exist; refusing to narrow the check';
 END IF;
END $$;
ALTER TABLE vendor_notification_outbox DROP CONSTRAINT IF EXISTS vendor_notification_outbox_type_check;
ALTER TABLE vendor_notification_outbox ADD CONSTRAINT vendor_notification_outbox_type_check CHECK (type IN (
    'vendor_approved', 'vendor_rejected', 'marketplace_policy_updated', 'shop_policy_approved', 'shop_policy_rejected',
    'return_destination_verified', 'return_destination_rejected'));
DROP TABLE IF EXISTS vendor_availability_audit_logs;
DROP TABLE IF EXISTS vendor_availability_intervals;
ALTER TABLE vendor_outbox DROP COLUMN IF EXISTS accepting_orders;
ALTER TABLE vendors DROP COLUMN IF EXISTS availability_version, DROP COLUMN IF EXISTS accepting_orders;
