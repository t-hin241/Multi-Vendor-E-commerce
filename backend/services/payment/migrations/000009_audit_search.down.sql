DROP TRIGGER IF EXISTS payment_audit_append_only ON payment_admin_audit;
DROP FUNCTION IF EXISTS payment_audit_append_only();
DROP INDEX IF EXISTS payment_audit_request_idx;
DROP INDEX IF EXISTS payment_audit_actor_idx;
DROP INDEX IF EXISTS payment_audit_recent_idx;
ALTER TABLE payment_admin_audit DROP COLUMN IF EXISTS request_id;
