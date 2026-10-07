-- Refuses to drop published policies: orders refer to them by id and hash.
-- Roll back by turning FEATURE_VERSIONED_POLICIES_ENABLED off instead.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM marketplace_policy_versions WHERE status = 'published')
    OR EXISTS (SELECT 1 FROM shop_policy_versions WHERE status = 'approved') THEN
  RAISE EXCEPTION 'published policy versions exist; keep migration 000010 and disable FEATURE_VERSIONED_POLICIES_ENABLED';
 END IF;
 IF EXISTS (SELECT 1 FROM vendor_notification_outbox WHERE type NOT IN ('vendor_approved', 'vendor_rejected'))
    OR EXISTS (SELECT 1 FROM vendor_audit_logs WHERE action IN ('shop_policy_approved', 'shop_policy_rejected')) THEN
  RAISE EXCEPTION 'policy notices or audit rows exist; keep migration 000010';
 END IF;
END $$;
ALTER TABLE vendor_notification_outbox DROP CONSTRAINT IF EXISTS vendor_notification_outbox_type_check;
ALTER TABLE vendor_notification_outbox ADD CONSTRAINT vendor_notification_outbox_type_check CHECK (type IN ('vendor_approved', 'vendor_rejected'));
ALTER TABLE vendor_audit_logs DROP CONSTRAINT vendor_audit_logs_action_check;
ALTER TABLE vendor_audit_logs ADD CONSTRAINT vendor_audit_logs_action_check CHECK(action IN
 ('approved','rejected','suspended','resubmitted','restored','payout_submitted','payout_verified','payout_rejected','payout_disabled',
  'payout_details_read','event_replayed'));
DROP TABLE IF EXISTS policy_audit_logs;
DROP TABLE IF EXISTS policy_outbox;
DROP TABLE IF EXISTS shop_policy_versions;
DROP TABLE IF EXISTS marketplace_policy_versions;
DROP FUNCTION IF EXISTS shop_policy_version_decided_once();
DROP FUNCTION IF EXISTS policy_version_immutable();
