-- Refuses while such notices exist: the old check cannot hold them.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM vendor_notification_outbox WHERE type IN ('return_destination_verified', 'return_destination_rejected')) THEN
        RAISE EXCEPTION 'return destination notices exist; refusing to narrow the check';
    END IF;
END $$;
ALTER TABLE vendor_notification_outbox DROP CONSTRAINT IF EXISTS vendor_notification_outbox_type_check;
ALTER TABLE vendor_notification_outbox ADD CONSTRAINT vendor_notification_outbox_type_check CHECK (type IN (
    'vendor_approved', 'vendor_rejected', 'marketplace_policy_updated', 'shop_policy_approved', 'shop_policy_rejected'));
