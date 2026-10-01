DROP TRIGGER IF EXISTS catalog_audit_append_only ON product_audit_logs;
DROP FUNCTION IF EXISTS catalog_audit_append_only();
DROP INDEX IF EXISTS catalog_audit_request_idx;
DROP INDEX IF EXISTS catalog_audit_actor_idx;
DROP INDEX IF EXISTS catalog_audit_recent_idx;
ALTER TABLE product_audit_logs DROP COLUMN IF EXISTS request_id;
