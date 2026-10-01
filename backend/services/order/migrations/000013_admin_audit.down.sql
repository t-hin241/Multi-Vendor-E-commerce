-- Refuses to drop audit rows written by this version: rolling back an
-- image never deletes audit history.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM order_admin_audit WHERE request_id IS NOT NULL) THEN
  RAISE EXCEPTION 'order_admin_audit holds audit written after the upgrade; keep migration 000013';
 END IF;
END $$;
DROP INDEX IF EXISTS order_refunds_idempotency_key;
ALTER TABLE order_refunds DROP COLUMN IF EXISTS idempotency_key;
DROP TABLE IF EXISTS order_admin_audit;
DROP FUNCTION IF EXISTS order_audit_append_only();
