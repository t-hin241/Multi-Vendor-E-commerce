DROP TRIGGER IF EXISTS review_audit_append_only ON review_moderation_audit_logs;
DROP FUNCTION IF EXISTS review_audit_append_only();
DROP INDEX IF EXISTS review_audit_request_idx;
DROP INDEX IF EXISTS review_audit_actor_idx;
DROP INDEX IF EXISTS review_audit_recent_idx;
ALTER TABLE review_moderation_audit_logs DROP COLUMN IF EXISTS request_id;
