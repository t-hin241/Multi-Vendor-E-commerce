-- Refuses once the carrier decided a destination: its audit rows would
-- break the narrower action list.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM vendor_audit_logs WHERE action IN ('return_destination_carrier_verified', 'return_destination_carrier_rejected')) THEN
        RAISE EXCEPTION 'carrier address checks were recorded; keep migration 000014';
    END IF;
END $$;
ALTER TABLE vendor_audit_logs DROP CONSTRAINT vendor_audit_logs_action_check;
ALTER TABLE vendor_audit_logs ADD CONSTRAINT vendor_audit_logs_action_check CHECK(action IN
 ('approved','rejected','suspended','resubmitted','restored','payout_submitted','payout_verified','payout_rejected','payout_disabled',
  'payout_details_read','event_replayed','shop_policy_approved','shop_policy_rejected',
  'return_destination_set','return_destination_verified','return_destination_rejected'));
DROP INDEX vendor_return_destinations_carrier_pending_idx;
ALTER TABLE vendor_return_destinations DROP CONSTRAINT vendor_return_destinations_carrier_check,
    DROP COLUMN carrier_checked_at, DROP COLUMN carrier_check_ref, DROP COLUMN carrier_check_reason,
    DROP COLUMN carrier_check_result, DROP COLUMN carrier_check_version;
