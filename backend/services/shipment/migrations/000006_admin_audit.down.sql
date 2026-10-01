-- Refuses to drop audit rows written by this version: rolling back an
-- image never deletes audit history.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM shipment_admin_audit WHERE request_id IS NOT NULL) THEN
  RAISE EXCEPTION 'shipment_admin_audit holds audit written after the upgrade; keep migration 000006';
 END IF;
END $$;
DROP TABLE IF EXISTS shipment_admin_audit;
DROP FUNCTION IF EXISTS shipment_audit_append_only();
