-- Refuses to drop staff history. Roll back by turning
-- FEATURE_SHOP_STAFF_ENABLED off: staff lose access, owners keep it.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM vendor_memberships WHERE role = 'staff')
    OR EXISTS (SELECT 1 FROM staff_invitations)
    OR EXISTS (SELECT 1 FROM membership_audit_logs) THEN
  RAISE EXCEPTION 'shop staff records exist; keep migration 000011 and disable FEATURE_SHOP_STAFF_ENABLED';
 END IF;
END $$;
DROP TRIGGER IF EXISTS vendors_owner_fixed ON vendors;
DROP TRIGGER IF EXISTS vendors_owner_membership ON vendors;
DROP FUNCTION IF EXISTS vendor_owner_fixed();
DROP FUNCTION IF EXISTS vendor_owner_membership();
DROP TABLE IF EXISTS membership_audit_logs;
DROP TABLE IF EXISTS staff_invitations;
DROP TABLE IF EXISTS membership_permissions;
DROP TABLE IF EXISTS vendor_memberships;
