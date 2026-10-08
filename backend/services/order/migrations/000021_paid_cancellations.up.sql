-- AF-03: cancelling a paid vendor order before handover. The buyer asks
-- (or the vendor reports it cannot fulfil); the request holds the payout in
-- Payment's ledger, an admin decides, Order stops the shipment, puts the
-- stock back once and refunds through Payment. Money stays paid until
-- Payment confirms the refund.
--
-- Fence: Shipment claims a fulfillment grant from Order before handover
-- (vendor_orders.handover_*). Claiming and opening a request both take the
-- order lock: either the claim wins (the request is refused, the package
-- ships) or the request wins (the claim is refused).

CREATE TABLE cancellation_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES orders (id),
    vendor_order_id UUID NOT NULL REFERENCES vendor_orders (id),
    vendor_id UUID NOT NULL,
    buyer_id UUID NOT NULL,
    origin TEXT NOT NULL CHECK (origin IN ('buyer', 'vendor')),
    requested_by UUID NOT NULL,
    reason_code TEXT NOT NULL CHECK (reason_code IN ('changed_mind', 'ordered_by_mistake', 'delivery_too_slow', 'other',
        'out_of_stock', 'damaged_stock', 'cannot_fulfil')),
    reason TEXT NOT NULL CHECK (length(reason) BETWEEN 1 AND 1000),
    status TEXT NOT NULL CHECK (status IN ('preparing', 'requested', 'stopping_fulfillment', 'approved', 'refund_pending', 'resolved',
        'rejected', 'needs_review')),
    policy_version TEXT NOT NULL CHECK (length(policy_version) BETWEEN 1 AND 40),
    -- Settlement hold in Payment's ledger (00 §6.1).
    hold_id UUID UNIQUE,
    hold_status TEXT CHECK (hold_status IN ('preparing', 'active', 'needs_review', 'releasing', 'released')),
    hold_note TEXT CHECK (hold_note IS NULL OR length(hold_note) <= 500),
    -- Shipment's answer to the stop: stopped (never handed over),
    -- handed_over, delivered.
    stop_result TEXT CHECK (stop_result IN ('stopped', 'handed_over', 'delivered')),
    -- Put the units back (they never left the warehouse); decided by the
    -- admin. Recovery ids are "cancellation:<request>:<item>".
    restock BOOLEAN,
    inventory_recovery_ref TEXT,
    refund_id UUID REFERENCES order_refunds (id),
    decided_by UUID,
    decided_at TIMESTAMPTZ,
    decision_reason TEXT CHECK (decision_reason IS NULL OR length(decision_reason) BETWEEN 1 AND 500),
    review_reason TEXT CHECK (review_reason IS NULL OR length(review_reason) <= 500),
    resolved_at TIMESTAMPTZ,
    idempotency_key TEXT CHECK (idempotency_key IS NULL OR idempotency_key ~ '^[A-Za-z0-9._:-]{8,100}$'),
    request_hash TEXT,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((hold_id IS NULL) = (hold_status IS NULL)),
    CHECK (status NOT IN ('approved', 'refund_pending', 'resolved') OR (decided_by IS NOT NULL AND stop_result = 'stopped')),
    CHECK (status <> 'refund_pending' OR refund_id IS NOT NULL)
);
-- One open request per vendor order; rejected and resolved ones stay as history.
CREATE UNIQUE INDEX cancellation_requests_open_idx ON cancellation_requests (vendor_order_id)
    WHERE status NOT IN ('rejected', 'resolved');
CREATE UNIQUE INDEX cancellation_requests_idempotency_idx ON cancellation_requests (requested_by, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX cancellation_requests_queue_idx ON cancellation_requests (status, created_at, id);
CREATE INDEX cancellation_requests_order_idx ON cancellation_requests (order_id);
CREATE INDEX cancellation_requests_vendor_idx ON cancellation_requests (vendor_id, created_at DESC);
CREATE INDEX cancellation_requests_refund_idx ON cancellation_requests (refund_id) WHERE refund_id IS NOT NULL;

CREATE TABLE cancellation_request_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id UUID NOT NULL REFERENCES cancellation_requests (id),
    actor_id UUID,
    actor_role TEXT NOT NULL CHECK (actor_role IN ('buyer', 'vendor', 'admin', 'system')),
    action TEXT NOT NULL CHECK (action ~ '^[a-z_]{1,60}$'),
    from_status TEXT,
    to_status TEXT NOT NULL,
    -- Shown to admins only.
    note TEXT CHECK (note IS NULL OR length(note) <= 1000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX cancellation_request_history_idx ON cancellation_request_history (request_id, created_at, id);
CREATE TRIGGER cancellation_request_history_append_only BEFORE UPDATE OR DELETE ON cancellation_request_history
FOR EACH ROW EXECUTE FUNCTION support_append_only();

CREATE FUNCTION cancellation_request_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'cancellation requests are never deleted';
    END IF;
    IF NEW.vendor_order_id <> OLD.vendor_order_id OR NEW.origin <> OLD.origin OR NEW.requested_by <> OLD.requested_by THEN
        RAISE EXCEPTION 'a cancellation request keeps its vendor order and requester';
    END IF;
    IF OLD.status IN ('rejected', 'resolved') AND NEW.status <> OLD.status THEN
        RAISE EXCEPTION 'a % cancellation request is final', OLD.status;
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER cancellation_request_guard BEFORE UPDATE OR DELETE ON cancellation_requests
FOR EACH ROW EXECUTE FUNCTION cancellation_request_guard();

-- The fulfillment grant Shipment claimed before handover.
ALTER TABLE vendor_orders
    ADD COLUMN handover_claimed_at TIMESTAMPTZ,
    ADD COLUMN handover_shipment_id UUID;

-- A cancellation refunds the vendor order through Payment.
ALTER TABLE order_refunds DROP CONSTRAINT IF EXISTS order_refunds_reason_code_check;
ALTER TABLE order_refunds ADD CONSTRAINT order_refunds_reason_code_check
    CHECK (reason_code IN ('return', 'dispute', 'late_payment', 'duplicate_payment', 'cancellation'));

ALTER TABLE order_effects DROP CONSTRAINT IF EXISTS order_effects_kind_check;
ALTER TABLE order_effects ADD CONSTRAINT order_effects_kind_check CHECK (kind IN (
    'create_shipment', 'cancel_shipment', 'release_inventory', 'notify', 'request_refund', 'restock_return', 'settle_vendor_order',
    'report_rejected_outcome', 'acquire_settlement_hold', 'release_settlement_hold', 'stop_fulfillment', 'recover_cancelled_stock'));

ALTER TABLE order_admin_audit DROP CONSTRAINT IF EXISTS order_admin_audit_entity_type_check;
ALTER TABLE order_admin_audit ADD CONSTRAINT order_admin_audit_entity_type_check
    CHECK (entity_type IN ('order', 'refund', 'return_request', 'effect', 'commission_rule', 'support_case', 'support_intake',
        'cancellation_request'));
