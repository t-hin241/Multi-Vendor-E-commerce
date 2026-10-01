DROP TRIGGER IF EXISTS identity_audit_append_only ON identity_audit_logs;
DROP FUNCTION IF EXISTS identity_audit_append_only();
DROP INDEX IF EXISTS identity_audit_request_idx;
DROP INDEX IF EXISTS identity_audit_actor_idx;
DROP INDEX IF EXISTS identity_audit_recent_idx;
ALTER TABLE identity_audit_logs DROP COLUMN IF EXISTS request_id;
