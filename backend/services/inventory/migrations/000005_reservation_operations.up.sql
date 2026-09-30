DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM stock_reservations GROUP BY order_id,inventory_item_id HAVING count(*)>1)
 OR EXISTS(SELECT 1 FROM stock_reservations GROUP BY order_id HAVING count(DISTINCT status)>1)
 THEN RAISE EXCEPTION 'Inventory legacy reservations require reconciliation before migration'; END IF;
END $$;

CREATE TABLE reservation_operations (
 order_id UUID PRIMARY KEY,
 payload_hash TEXT,
 status TEXT NOT NULL CHECK(status IN ('held','committed','released','expired')),
 expires_at TIMESTAMPTZ NOT NULL,
 legacy BOOLEAN NOT NULL DEFAULT false,
 expiry_attempts INT NOT NULL DEFAULT 0,
 expiry_next_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 reconciled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 reconciliation_issue TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO reservation_operations(order_id,status,expires_at,legacy,created_at)
 SELECT order_id,CASE min(status) WHEN 'active' THEN 'held' ELSE min(status) END,min(expires_at),true,min(created_at)
 FROM stock_reservations GROUP BY order_id;
ALTER TABLE stock_reservations DROP CONSTRAINT stock_reservations_status_check;
ALTER TABLE stock_reservations ADD CONSTRAINT stock_reservations_status_check CHECK(status IN ('active','committed','released','expired'));
ALTER TABLE stock_reservations ADD CONSTRAINT reservation_operation_fk FOREIGN KEY(order_id) REFERENCES reservation_operations(order_id);
ALTER TABLE stock_reservations ADD CONSTRAINT reservation_operation_line_unique UNIQUE(order_id,inventory_item_id);
CREATE INDEX reservation_operations_expiry_idx ON reservation_operations(expires_at,order_id) WHERE status='held' AND NOT legacy;
CREATE INDEX reservation_operations_reconcile_idx ON reservation_operations(reconciled_at,order_id);
ALTER TABLE stock_movements ADD COLUMN actor_user_id UUID;
ALTER TABLE stock_movements ADD COLUMN reserved_change BIGINT NOT NULL DEFAULT 0;
ALTER TABLE stock_movements ADD COLUMN operation_key TEXT;
CREATE UNIQUE INDEX stock_movements_operation_unique ON stock_movements(inventory_item_id,operation_key) WHERE operation_key IS NOT NULL;
ALTER TABLE inventory_items ADD CONSTRAINT inventory_total_quantity_safe CHECK(available_quantity::numeric+reserved_quantity::numeric<=9223372036854775807) NOT VALID;
CREATE TABLE inventory_outbox (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 order_id UUID NOT NULL REFERENCES reservation_operations(order_id),
 event_type TEXT NOT NULL CHECK(event_type='ReservationExpired'),
 attempts INT NOT NULL DEFAULT 0,
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 delivered_at TIMESTAMPTZ,
 last_error TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(order_id,event_type)
);
CREATE INDEX inventory_outbox_due_idx ON inventory_outbox(next_attempt_at,id) WHERE delivered_at IS NULL AND attempts<10;
CREATE TABLE inventory_operation_audit (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 order_id UUID NOT NULL REFERENCES reservation_operations(order_id),
 actor_user_id UUID NOT NULL,
 action TEXT NOT NULL,
 reason TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE inventory_stock_outbox (
 variant_id UUID PRIMARY KEY,
 attempts INT NOT NULL DEFAULT 0,
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE FUNCTION inventory_queue_stock_cache() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.variant_id IS NOT NULL THEN
  INSERT INTO inventory_stock_outbox(variant_id) VALUES(NEW.variant_id)
  ON CONFLICT(variant_id) DO UPDATE SET attempts=0,next_attempt_at=now();
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER inventory_stock_cache_changed AFTER INSERT OR UPDATE OF available_quantity,reserved_quantity ON inventory_items FOR EACH ROW EXECUTE FUNCTION inventory_queue_stock_cache();
