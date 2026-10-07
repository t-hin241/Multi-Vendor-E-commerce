-- Refuses to drop grant history. Roll back by turning
-- FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED off (every admin keeps every
-- bundle, as before AF-19).
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM admin_permission_grants) THEN
  RAISE EXCEPTION 'admin permission grants exist; keep migration 000005 and disable FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED';
 END IF;
END $$;
DROP TABLE IF EXISTS reauth_proofs;
DROP TABLE IF EXISTS admin_permission_grants;
ALTER TABLE users DROP COLUMN IF EXISTS permission_version;
