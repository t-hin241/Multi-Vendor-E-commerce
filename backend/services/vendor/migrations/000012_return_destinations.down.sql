-- Refuse while destinations exist: open returns name them. Order keeps
-- its own snapshots, but new returns would lose their destination.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM vendor_return_destinations) THEN
        RAISE EXCEPTION 'return destinations exist; turn FEATURE_RETURN_SHIPPING_ENABLED off instead of migrating down';
    END IF;
END $$;

-- Fails while audit rows of the new actions exist (audit is append-only).
ALTER TABLE vendor_audit_logs DROP CONSTRAINT vendor_audit_logs_action_check;
ALTER TABLE vendor_audit_logs ADD CONSTRAINT vendor_audit_logs_action_check CHECK(action IN
 ('approved','rejected','suspended','resubmitted','restored','payout_submitted','payout_verified','payout_rejected','payout_disabled',
  'payout_details_read','event_replayed','shop_policy_approved','shop_policy_rejected'));

DROP TABLE vendor_return_destinations;
