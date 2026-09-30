-- ORD-01/02/03: checkout idempotency, atomic shipping + commission
-- snapshots, order versions (CAS), a payment capture ledger and durable
-- side-effect tasks. Additive only; history is never recomputed.

-- Commission rules get an explicit version (insert-only ledger).
ALTER TABLE commission_rules ADD COLUMN version BIGINT;
UPDATE commission_rules r SET version = v.rn
FROM (SELECT id, row_number() OVER (ORDER BY created_at, id) AS rn FROM commission_rules) v
WHERE v.id = r.id;
ALTER TABLE commission_rules ALTER COLUMN version SET NOT NULL;
CREATE UNIQUE INDEX commission_rules_version_key ON commission_rules (version);
-- New rules (including ones an older Order image inserts during rollout)
-- take the next version from a sequence.
CREATE SEQUENCE commission_rules_version_seq OWNED BY commission_rules.version;
SELECT setval('commission_rules_version_seq', COALESCE((SELECT max(version) FROM commission_rules), 0) + 1, false);
ALTER TABLE commission_rules ALTER COLUMN version SET DEFAULT nextval('commission_rules_version_seq');

ALTER TABLE orders
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1,
    -- 'preparing' until stock is reserved; Payment may only open an intent
    -- for a 'ready' order. Existing orders were complete when created.
    ADD COLUMN checkout_state TEXT NOT NULL DEFAULT 'ready' CHECK (checkout_state IN ('preparing', 'ready', 'failed')),
    ADD COLUMN subtotal_amount BIGINT,
    ADD COLUMN shipping_amount BIGINT NOT NULL DEFAULT 0 CHECK (shipping_amount >= 0),
    ADD COLUMN refunded_amount BIGINT NOT NULL DEFAULT 0 CHECK (refunded_amount >= 0),
    ADD COLUMN paid_at TIMESTAMPTZ;

UPDATE orders o SET subtotal_amount = s.subtotal, shipping_amount = s.shipping
FROM (SELECT order_id, sum(subtotal_amount) AS subtotal, sum(shipping_fee_amount) AS shipping
      FROM vendor_orders GROUP BY order_id) s
WHERE s.order_id = o.id;
UPDATE orders SET subtotal_amount = total_amount WHERE subtotal_amount IS NULL;
ALTER TABLE orders ALTER COLUMN subtotal_amount SET NOT NULL;
ALTER TABLE orders ADD CONSTRAINT orders_subtotal_non_negative CHECK (subtotal_amount >= 0);
-- An older Order image (rollout/rollback) inserts orders without the
-- subtotal; derive it instead of failing the insert.
CREATE FUNCTION orders_fill_subtotal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.subtotal_amount IS NULL THEN
  NEW.subtotal_amount := NEW.total_amount - NEW.shipping_amount;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER orders_fill_subtotal BEFORE INSERT ON orders FOR EACH ROW EXECUTE FUNCTION orders_fill_subtotal();

ALTER TABLE vendor_orders
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1,
    -- Shipping quote snapshot taken before the order is created. NULL for
    -- orders created before this migration (their fee was best-effort).
    ADD COLUMN shipping_carrier_id UUID,
    ADD COLUMN shipping_zone_id UUID,
    ADD COLUMN shipping_fee_rule_id UUID,
    ADD COLUMN shipping_fee_rule_version INTEGER,
    ADD COLUMN package_weight_grams BIGINT CHECK (package_weight_grams >= 0),
    ADD COLUMN shipping_quoted_at TIMESTAMPTZ,
    -- Commission snapshot. 'checkout' = taken with the order;
    -- 'payment_time_legacy' = taken at payment time by the old flow.
    ADD COLUMN commission_rule_id UUID REFERENCES commission_rules (id),
    ADD COLUMN commission_rule_version BIGINT,
    ADD COLUMN commission_base_amount BIGINT CHECK (commission_base_amount >= 0),
    ADD COLUMN commission_rounding TEXT CHECK (commission_rounding IN ('floor')),
    ADD COLUMN commission_source TEXT CHECK (commission_source IN ('checkout', 'payment_time_legacy')),
    ADD COLUMN refunded_amount BIGINT NOT NULL DEFAULT 0 CHECK (refunded_amount >= 0),
    ADD COLUMN completed_at TIMESTAMPTZ;

UPDATE vendor_orders SET commission_source = 'payment_time_legacy', commission_base_amount = subtotal_amount
WHERE commission_rate_bps IS NOT NULL;
-- Legacy completion time is only known as the row's last update.
UPDATE vendor_orders SET completed_at = updated_at WHERE status = 'completed';

-- One checkout operation per (buyer, Idempotency-Key). A retry with the same
-- key and request returns the same outcome; a different request is refused.
CREATE TABLE checkout_operations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    buyer_id UUID NOT NULL,
    idempotency_key TEXT NOT NULL CHECK (length(idempotency_key) BETWEEN 8 AND 128),
    request_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'preparing' CHECK (status IN ('preparing', 'completed', 'failed')),
    order_id UUID REFERENCES orders (id),
    error_code TEXT,
    error_message TEXT,
    error_status INTEGER,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (buyer_id, idempotency_key)
);
CREATE INDEX checkout_operations_preparing_idx ON checkout_operations (updated_at) WHERE status = 'preparing';
CREATE INDEX checkout_operations_expires_idx ON checkout_operations (expires_at);

-- Every capture Payment reports, applied or not. A rejected capture (order
-- cancelled, amount mismatch, second payment) is money to refund or review.
CREATE TABLE order_payments (
    payment_id UUID PRIMARY KEY,
    order_id UUID NOT NULL REFERENCES orders (id),
    amount BIGINT NOT NULL CHECK (amount > 0),
    currency TEXT NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    outcome TEXT NOT NULL CHECK (outcome IN ('applied', 'rejected')),
    rejection_reason TEXT,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((outcome = 'rejected') = (rejection_reason IS NOT NULL))
);
CREATE INDEX order_payments_order_idx ON order_payments (order_id);
CREATE INDEX order_payments_rejected_idx ON order_payments (received_at) WHERE outcome = 'rejected';

-- Durable side effects of order transitions, written in the same
-- transaction as the transition and retried by the worker.
CREATE TABLE order_effects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES orders (id),
    kind TEXT NOT NULL CHECK (kind IN ('create_shipment', 'cancel_shipment', 'release_inventory', 'notify', 'request_refund', 'restock_return')),
    target TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done', 'parked')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    done_at TIMESTAMPTZ,
    UNIQUE (order_id, kind, target)
);
CREATE INDEX order_effects_due_idx ON order_effects (next_attempt_at) WHERE status = 'pending';
CREATE INDEX order_effects_order_idx ON order_effects (order_id);
