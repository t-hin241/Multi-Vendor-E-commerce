ALTER TABLE inventory_items ADD COLUMN version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE inventory_items ADD COLUMN low_stock_threshold BIGINT NOT NULL DEFAULT 0 CHECK(low_stock_threshold>=0);
ALTER TABLE inventory_items ADD COLUMN alert_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE inventory_items ADD COLUMN alert_active BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE inventory_items ADD COLUMN alert_episode BIGINT NOT NULL DEFAULT 0;
ALTER TABLE stock_movements ADD COLUMN source TEXT;
ALTER TABLE stock_movements ADD COLUMN adjustment_reason TEXT;
ALTER TABLE stock_movements ADD COLUMN evidence_ref TEXT;
CREATE INDEX stock_movements_history_idx ON stock_movements(inventory_item_id,created_at DESC,id DESC);
CREATE TRIGGER stock_movements_append_only BEFORE UPDATE OR DELETE ON stock_movements
 FOR EACH ROW EXECUTE FUNCTION inventory_audit_append_only();

CREATE TABLE inventory_vendor_policy (
 vendor_id UUID PRIMARY KEY, enabled BOOLEAN NOT NULL DEFAULT false,
 receipt_limit BIGINT NOT NULL DEFAULT 10000 CHECK(receipt_limit BETWEEN 1 AND 10000),
 version BIGINT NOT NULL DEFAULT 0
);
CREATE TABLE stock_adjustment_operations (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), vendor_id UUID NOT NULL,
 item_id UUID NOT NULL REFERENCES inventory_items(id), actor_user_id UUID NOT NULL,
 operation_key TEXT NOT NULL, payload_hash TEXT NOT NULL, policy_version BIGINT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('receipt','damage','correction','count')),
 quantity_delta BIGINT NOT NULL, physical_count BIGINT, reason TEXT NOT NULL, evidence_ref TEXT NOT NULL,
 expected_version BIGINT NOT NULL, status TEXT NOT NULL CHECK(status IN ('applied','approval_required','reserved_stock_conflict','rejected','resolved')),
 first_approved_by UUID, receipt JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(vendor_id,operation_key)
);
CREATE INDEX stock_adjustment_queue_idx ON stock_adjustment_operations(status,created_at,id);
CREATE TABLE inventory_self_service_receipts (
 scope TEXT NOT NULL, operation_key TEXT NOT NULL, payload_hash TEXT NOT NULL,
 receipt JSONB NOT NULL, PRIMARY KEY(scope,operation_key)
);
CREATE TRIGGER inventory_self_service_receipts_append_only BEFORE UPDATE OR DELETE ON inventory_self_service_receipts
 FOR EACH ROW EXECUTE FUNCTION inventory_audit_append_only();
CREATE TABLE stock_alert_episode (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), item_id UUID NOT NULL REFERENCES inventory_items(id),
 vendor_id UUID NOT NULL, episode_version BIGINT NOT NULL, item_version BIGINT NOT NULL,
 attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 delivered_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(item_id,episode_version)
);
CREATE INDEX stock_alert_due_idx ON stock_alert_episode(next_attempt_at,id) WHERE delivered_at IS NULL AND attempts<5;

-- Every writer, including legacy restock, reserve, commit and recovery, uses
-- the same item lock and advances the version. No synthetic movements.
CREATE FUNCTION inventory_self_service_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 NEW.version := OLD.version + 1;
 IF NEW.available_quantity >= NEW.low_stock_threshold AND NEW.available_quantity > OLD.available_quantity THEN
  NEW.alert_active := false;
 ELSIF NEW.alert_enabled AND NEW.available_quantity < NEW.low_stock_threshold AND NOT OLD.alert_active THEN
  NEW.alert_active := true;
  NEW.alert_episode := OLD.alert_episode + 1;
  INSERT INTO stock_alert_episode(item_id,vendor_id,episode_version,item_version)
   VALUES(NEW.id,NEW.vendor_id,NEW.alert_episode,NEW.version);
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER inventory_self_service_changed BEFORE UPDATE ON inventory_items
 FOR EACH ROW EXECUTE FUNCTION inventory_self_service_version();
