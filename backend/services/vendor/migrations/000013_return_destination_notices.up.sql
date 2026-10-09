-- PW-009: the owner is told when an admin verifies or rejects the shop's
-- return destination (one notice per destination version and decision).
ALTER TABLE vendor_notification_outbox DROP CONSTRAINT IF EXISTS vendor_notification_outbox_type_check;
ALTER TABLE vendor_notification_outbox ADD CONSTRAINT vendor_notification_outbox_type_check CHECK (type IN (
    'vendor_approved', 'vendor_rejected', 'marketplace_policy_updated', 'shop_policy_approved', 'shop_policy_rejected',
    'return_destination_verified', 'return_destination_rejected'));
