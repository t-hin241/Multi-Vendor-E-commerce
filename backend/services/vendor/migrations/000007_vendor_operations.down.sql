DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM vendors WHERE status='suspended') THEN RAISE EXCEPTION 'Cannot remove suspended status while suspended shops exist'; END IF;
END $$;
DROP TRIGGER payout_account_version_immutable ON vendor_payout_accounts;
DROP FUNCTION protect_payout_account_version();

DROP TABLE vendor_payout_details_audit;
DROP INDEX vendor_payout_accounts_version_idx;
ALTER TABLE vendor_payout_accounts DROP COLUMN version;
DROP TABLE vendor_outbox;
ALTER TABLE vendor_audit_logs DROP COLUMN version;
DROP INDEX vendors_owner_status_idx;
ALTER TABLE vendors DROP COLUMN version, DROP COLUMN enforced_version, DROP COLUMN suspension_reason;
ALTER TABLE vendors DROP CONSTRAINT vendors_status_check;
ALTER TABLE vendors ADD CONSTRAINT vendors_status_check CHECK(status IN ('pending','approved','rejected'));
