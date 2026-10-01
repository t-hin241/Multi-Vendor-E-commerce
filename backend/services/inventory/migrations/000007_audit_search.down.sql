DROP TRIGGER IF EXISTS inventory_audit_append_only ON inventory_operation_audit;
DROP FUNCTION IF EXISTS inventory_audit_append_only();
DROP INDEX IF EXISTS inventory_audit_request_idx;
DROP INDEX IF EXISTS inventory_audit_actor_idx;
DROP INDEX IF EXISTS inventory_audit_recent_idx;
ALTER TABLE inventory_operation_audit DROP COLUMN IF EXISTS request_id;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM inventory_operation_audit WHERE order_id IS NULL) THEN
  RAISE EXCEPTION 'restock decisions are audited here; keep migration 000007';
 END IF;
END $$;
DROP INDEX IF EXISTS inventory_audit_entity_idx;
ALTER TABLE inventory_operation_audit DROP CONSTRAINT IF EXISTS inventory_operation_audit_entity_check;
ALTER TABLE inventory_operation_audit DROP COLUMN IF EXISTS changes;
ALTER TABLE inventory_operation_audit DROP COLUMN IF EXISTS entity_id;
ALTER TABLE inventory_operation_audit DROP COLUMN IF EXISTS entity_type;
ALTER TABLE inventory_operation_audit ALTER COLUMN order_id SET NOT NULL;
ALTER TABLE inventory_operation_audit ALTER COLUMN reason SET NOT NULL;
