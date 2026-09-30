-- ORD-04/05: refunds are requested through Payment and only count as money
-- returned once Payment confirms them; returns get a versioned policy,
-- quantity, evidence, a goods-received step and an audit trail.

CREATE TABLE order_refunds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES orders (id),
    vendor_order_id UUID REFERENCES vendor_orders (id),
    return_request_id UUID REFERENCES return_requests (id),
    -- The capture being refunded, when it is a rejected (late/duplicate) one.
    payment_id UUID REFERENCES order_payments (payment_id),
    reason_code TEXT NOT NULL CHECK (reason_code IN ('return', 'dispute', 'late_payment', 'duplicate_payment')),
    amount BIGINT NOT NULL CHECK (amount > 0),
    currency TEXT NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    reason TEXT NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
    -- requested: waiting for Payment to accept; submitted: Payment accepted
    -- and is refunding; succeeded/failed: Payment's confirmed outcome;
    -- rejected: Payment refused the request (e.g. over the captured amount).
    status TEXT NOT NULL DEFAULT 'requested' CHECK (status IN ('requested', 'submitted', 'succeeded', 'failed', 'rejected')),
    payment_refund_id TEXT,
    failure_reason TEXT,
    requested_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ
);
CREATE INDEX order_refunds_order_idx ON order_refunds (order_id, created_at DESC);
CREATE INDEX order_refunds_open_idx ON order_refunds (status) WHERE status IN ('requested', 'submitted');
-- At most one open refund per return request and per rejected capture.
CREATE UNIQUE INDEX order_refunds_open_return_key ON order_refunds (return_request_id)
    WHERE return_request_id IS NOT NULL AND status IN ('requested', 'submitted', 'succeeded');
CREATE UNIQUE INDEX order_refunds_open_payment_key ON order_refunds (payment_id)
    WHERE payment_id IS NOT NULL AND status IN ('requested', 'submitted', 'succeeded');

ALTER TABLE return_requests DROP CONSTRAINT IF EXISTS return_requests_status_check;
-- 'approved_awaiting_provider_refund' meant "approved, nothing refunded
-- yet"; it becomes 'approved' (goods not yet received), never 'refunded'.
UPDATE return_requests SET status = 'approved' WHERE status = 'approved_awaiting_provider_refund';
ALTER TABLE return_requests ADD CONSTRAINT return_requests_status_check CHECK (status IN (
    'requested', 'vendor_confirmed', 'rejected', 'approved', 'received', 'refund_pending', 'refunded', 'refund_failed'));

-- Partial returns: an item may be returned in several requests as long as
-- the non-rejected quantities stay within the purchased quantity (checked
-- by Order under the order lock). The database still allows only one open
-- request per item, which also guards an older Order image during rollout.
ALTER TABLE return_requests DROP CONSTRAINT IF EXISTS return_requests_order_item_id_key;
CREATE UNIQUE INDEX return_requests_open_item_key ON return_requests (order_item_id)
    WHERE status NOT IN ('rejected', 'refunded');
CREATE INDEX return_requests_item_idx ON return_requests (order_item_id);

ALTER TABLE return_requests
    ADD COLUMN quantity BIGINT CHECK (quantity > 0),
    ADD COLUMN refund_amount BIGINT CHECK (refund_amount > 0),
    ADD COLUMN policy_version TEXT,
    ADD COLUMN return_window_days INTEGER CHECK (return_window_days > 0),
    ADD COLUMN evidence TEXT CHECK (length(evidence) <= 2000),
    ADD COLUMN vendor_note TEXT CHECK (length(vendor_note) <= 1000),
    ADD COLUMN received_by UUID,
    ADD COLUMN received_at TIMESTAMPTZ,
    ADD COLUMN inspection_note TEXT CHECK (length(inspection_note) <= 1000),
    ADD COLUMN restock BOOLEAN,
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1;

-- Legacy requests covered the whole line.
UPDATE return_requests rr SET quantity = oi.quantity, refund_amount = oi.subtotal_amount, policy_version = 'legacy'
FROM order_items oi WHERE oi.id = rr.order_item_id;
ALTER TABLE return_requests ALTER COLUMN quantity SET NOT NULL;
ALTER TABLE return_requests ALTER COLUMN refund_amount SET NOT NULL;
ALTER TABLE return_requests ALTER COLUMN policy_version SET NOT NULL;
-- An older Order image inserts whole-line returns without these fields.
CREATE FUNCTION return_requests_fill_legacy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.quantity IS NULL OR NEW.refund_amount IS NULL THEN
  SELECT coalesce(NEW.quantity, oi.quantity), coalesce(NEW.refund_amount, oi.subtotal_amount)
    INTO NEW.quantity, NEW.refund_amount FROM order_items oi WHERE oi.id = NEW.order_item_id;
 END IF;
 NEW.policy_version := coalesce(NEW.policy_version, 'legacy');
 RETURN NEW;
END $$;
CREATE TRIGGER return_requests_fill_legacy BEFORE INSERT ON return_requests FOR EACH ROW EXECUTE FUNCTION return_requests_fill_legacy();

CREATE TABLE return_request_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    return_request_id UUID NOT NULL REFERENCES return_requests (id),
    actor_user_id UUID,
    actor_role TEXT NOT NULL CHECK (actor_role IN ('buyer', 'vendor', 'admin', 'system')),
    action TEXT NOT NULL,
    from_status TEXT,
    to_status TEXT NOT NULL,
    note TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX return_request_events_request_idx ON return_request_events (return_request_id, created_at);

-- Record the migration itself as the first audit entry of legacy requests.
INSERT INTO return_request_events (return_request_id, actor_role, action, to_status, note)
SELECT id, 'system', 'migrated', status, 'Imported by migration 000011' FROM return_requests;
