DROP TRIGGER IF EXISTS vendor_audit_append_only ON vendor_audit_logs;
DROP FUNCTION IF EXISTS vendor_audit_append_only();
DROP INDEX IF EXISTS vendor_audit_request_idx;
DROP INDEX IF EXISTS vendor_audit_actor_idx;
DROP INDEX IF EXISTS vendor_audit_recent_idx;
ALTER TABLE vendor_audit_logs DROP COLUMN IF EXISTS request_id;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM vendor_audit_logs WHERE action = 'event_replayed') THEN
  RAISE EXCEPTION 'event_replayed audit rows exist; keep migration 000008';
 END IF;
END $$;
ALTER TABLE vendor_audit_logs DROP CONSTRAINT vendor_audit_logs_action_check;
ALTER TABLE vendor_audit_logs ADD CONSTRAINT vendor_audit_logs_action_check CHECK(action IN
 ('approved','rejected','suspended','resubmitted','restored','payout_submitted','payout_verified','payout_rejected','payout_disabled','payout_details_read'));
