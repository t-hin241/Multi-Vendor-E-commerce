-- Refuses to drop approval history. Roll back by turning
-- FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED off (direct operator actions
-- come back, still audited).
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM approval_requests) THEN
  RAISE EXCEPTION 'approval requests exist; keep migration 000012 and disable FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED';
 END IF;
END $$;
DROP TABLE IF EXISTS approval_requests;
DROP FUNCTION IF EXISTS approval_request_immutable();
