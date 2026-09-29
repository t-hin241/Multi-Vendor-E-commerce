ALTER TABLE vendors DROP CONSTRAINT vendors_status_check;
ALTER TABLE vendors ADD CONSTRAINT vendors_status_check CHECK(status IN ('pending','approved','rejected','suspended'));
ALTER TABLE vendors ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK(version>0),
 ADD COLUMN enforced_version BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN suspension_reason TEXT;
CREATE INDEX vendors_owner_status_idx ON vendors(user_id,status,id);
ALTER TABLE vendor_audit_logs DROP CONSTRAINT vendor_audit_logs_action_check;
ALTER TABLE vendor_audit_logs ADD CONSTRAINT vendor_audit_logs_action_check CHECK(action IN
 ('approved','rejected','suspended','resubmitted','restored','payout_submitted','payout_verified','payout_rejected','payout_disabled','payout_details_read'));
ALTER TABLE vendor_audit_logs ADD COLUMN version BIGINT;
CREATE TABLE vendor_outbox (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), vendor_id UUID NOT NULL REFERENCES vendors(id),
 version BIGINT NOT NULL, status TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 lease_until TIMESTAMPTZ, delivered_at TIMESTAMPTZ,
 catalog_delivered BOOLEAN NOT NULL DEFAULT false, order_delivered BOOLEAN NOT NULL DEFAULT false,
 UNIQUE(vendor_id,version)
);
CREATE INDEX vendor_outbox_due_idx ON vendor_outbox(next_attempt_at) WHERE delivered_at IS NULL;
INSERT INTO vendor_outbox(vendor_id,version,status) SELECT id,version,status FROM vendors;
ALTER TABLE vendor_payout_accounts ADD COLUMN version BIGINT;
WITH numbered AS (SELECT id,row_number() OVER(PARTITION BY vendor_id ORDER BY created_at,id) AS n FROM vendor_payout_accounts)
 UPDATE vendor_payout_accounts p SET version=n.n FROM numbered n WHERE n.id=p.id;
ALTER TABLE vendor_payout_accounts ALTER COLUMN version SET NOT NULL;
ALTER TABLE vendor_payout_accounts ADD CONSTRAINT vendor_payout_version_positive CHECK(version>0);
CREATE UNIQUE INDEX vendor_payout_accounts_version_idx ON vendor_payout_accounts(vendor_id,version);
CREATE TABLE vendor_payout_details_audit (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), account_id UUID NOT NULL REFERENCES vendor_payout_accounts(id),
 actor_id UUID, service_scope TEXT, purpose TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE FUNCTION protect_payout_account_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.vendor_id,NEW.version,NEW.bank_bin,NEW.account_number_ciphertext,NEW.account_name_ciphertext,NEW.account_number_last4)
 IS DISTINCT FROM (OLD.vendor_id,OLD.version,OLD.bank_bin,OLD.account_number_ciphertext,OLD.account_name_ciphertext,OLD.account_number_last4) THEN
  RAISE EXCEPTION 'Payout destination versions are immutable';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER payout_account_version_immutable BEFORE UPDATE ON vendor_payout_accounts
FOR EACH ROW EXECUTE FUNCTION protect_payout_account_version();
