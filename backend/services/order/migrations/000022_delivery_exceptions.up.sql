-- AF-04: failed delivery and returned goods. Shipment reports that delivery
-- failed for good (attempt limit, returned, lost); Order opens one delivery
-- exception per vendor order at a time, holds the payout in Payment's
-- ledger, and an admin resolves it: a redelivery the buyer agreed to (a new
-- Shipment attempt, no new charge) or a refund. The shop records what came
-- back (sellable / damaged / missing); only sellable units go back to stock,
-- once per item. A shipment status alone never proves goods are sellable.

CREATE TABLE delivery_exceptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES orders (id),
    vendor_order_id UUID NOT NULL REFERENCES vendor_orders (id),
    vendor_id UUID NOT NULL,
    buyer_id UUID NOT NULL,
    -- The attempt that first failed (one exception per shipment), and the
    -- attempt the case follows now (a redelivery that failed again).
    shipment_id UUID NOT NULL UNIQUE,
    exception_type TEXT NOT NULL CHECK (exception_type IN ('attempts_exhausted', 'returned', 'lost')),
    current_shipment_id UUID NOT NULL,
    attempt_no INTEGER NOT NULL CHECK (attempt_no BETWEEN 1 AND 10),
    -- The latest carrier fact for the current attempt.
    carrier_outcome TEXT NOT NULL CHECK (carrier_outcome IN ('attempts_exhausted', 'returned', 'lost', 'delivered')),
    failed_attempts INTEGER NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
    detection_reason TEXT CHECK (detection_reason IS NULL OR length(detection_reason) <= 500),
    status TEXT NOT NULL CHECK (status IN ('investigating', 'awaiting_goods', 'awaiting_buyer', 'redelivery_pending', 'refund_pending',
        'needs_review', 'resolved')),
    -- Chosen branch: once refund is chosen no redelivery is created.
    resolution TEXT CHECK (resolution IN ('redelivery', 'refund', 'closed')),
    policy_snapshot JSONB NOT NULL,
    -- Settlement hold in Payment's ledger (00 §6.1).
    hold_id UUID UNIQUE,
    hold_status TEXT CHECK (hold_status IN ('preparing', 'active', 'needs_review', 'releasing', 'released')),
    hold_note TEXT CHECK (hold_note IS NULL OR length(hold_note) <= 500),
    redelivery_count INTEGER NOT NULL DEFAULT 0 CHECK (redelivery_count >= 0),
    replacement_shipment_id UUID,
    -- The address the buyer agreed to for the redelivery (kept like the
    -- order's own address snapshot).
    redelivery_address JSONB,
    consented_at TIMESTAMPTZ,
    refund_id UUID REFERENCES order_refunds (id),
    -- Sellable units go back once per item: "delivery_exception:<id>:<item>".
    inventory_recovery_ref TEXT,
    decided_by UUID,
    decided_at TIMESTAMPTZ,
    decision_reason TEXT CHECK (decision_reason IS NULL OR length(decision_reason) BETWEEN 1 AND 500),
    review_reason TEXT CHECK (review_reason IS NULL OR length(review_reason) <= 500),
    -- The carrier reported delivered after the case opened (never settles
    -- the case by itself).
    late_delivery_at TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((hold_id IS NULL) = (hold_status IS NULL)),
    CHECK (status <> 'refund_pending' OR refund_id IS NOT NULL),
    CHECK (status <> 'redelivery_pending' OR (resolution = 'redelivery' AND redelivery_address IS NOT NULL))
);
-- One open case per vendor order; resolved ones stay as history.
CREATE UNIQUE INDEX delivery_exceptions_open_idx ON delivery_exceptions (vendor_order_id) WHERE status <> 'resolved';
CREATE INDEX delivery_exceptions_queue_idx ON delivery_exceptions (status, created_at, id);
CREATE INDEX delivery_exceptions_order_idx ON delivery_exceptions (order_id);
CREATE INDEX delivery_exceptions_vendor_idx ON delivery_exceptions (vendor_id, created_at DESC);
CREATE INDEX delivery_exceptions_refund_idx ON delivery_exceptions (refund_id) WHERE refund_id IS NOT NULL;
CREATE INDEX delivery_exceptions_replacement_idx ON delivery_exceptions (replacement_shipment_id) WHERE replacement_shipment_id IS NOT NULL;

CREATE TABLE delivery_exception_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    exception_id UUID NOT NULL REFERENCES delivery_exceptions (id),
    actor_id UUID,
    actor_role TEXT NOT NULL CHECK (actor_role IN ('buyer', 'vendor', 'admin', 'system')),
    action TEXT NOT NULL CHECK (action ~ '^[a-z_]{1,60}$'),
    from_status TEXT,
    to_status TEXT NOT NULL,
    -- Shown to admins only.
    note TEXT CHECK (note IS NULL OR length(note) <= 1000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX delivery_exception_history_idx ON delivery_exception_history (exception_id, created_at, id);
CREATE TRIGGER delivery_exception_history_append_only BEFORE UPDATE OR DELETE ON delivery_exception_history
FOR EACH ROW EXECUTE FUNCTION support_append_only();

-- What the shop received back for one attempt. Immutable; an admin's
-- correction is a new version before stock is put back.
CREATE TABLE delivery_exception_receipts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    exception_id UUID NOT NULL REFERENCES delivery_exceptions (id),
    shipment_id UUID NOT NULL,
    receipt_version INTEGER NOT NULL CHECK (receipt_version >= 1),
    recorded_by UUID NOT NULL,
    actor_role TEXT NOT NULL CHECK (actor_role IN ('vendor', 'admin')),
    note TEXT CHECK (note IS NULL OR length(note) <= 1000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (exception_id, shipment_id, receipt_version)
);
CREATE TABLE delivery_exception_receipt_lines (
    receipt_id UUID NOT NULL REFERENCES delivery_exception_receipts (id),
    order_item_id UUID NOT NULL REFERENCES order_items (id),
    condition TEXT NOT NULL CHECK (condition IN ('sellable', 'damaged', 'missing')),
    quantity BIGINT NOT NULL CHECK (quantity > 0),
    PRIMARY KEY (receipt_id, order_item_id, condition)
);
CREATE TRIGGER delivery_exception_receipts_append_only BEFORE UPDATE OR DELETE ON delivery_exception_receipts
FOR EACH ROW EXECUTE FUNCTION support_append_only();
CREATE TRIGGER delivery_exception_receipt_lines_append_only BEFORE UPDATE OR DELETE ON delivery_exception_receipt_lines
FOR EACH ROW EXECUTE FUNCTION support_append_only();

CREATE FUNCTION delivery_exception_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'delivery exceptions are never deleted';
    END IF;
    IF NEW.vendor_order_id <> OLD.vendor_order_id OR NEW.shipment_id <> OLD.shipment_id OR NEW.exception_type <> OLD.exception_type THEN
        RAISE EXCEPTION 'a delivery exception keeps its vendor order and first shipment';
    END IF;
    IF OLD.status = 'resolved' AND NEW.status <> OLD.status THEN
        RAISE EXCEPTION 'a resolved delivery exception is final';
    END IF;
    IF OLD.resolution = 'refund' AND NEW.resolution IS DISTINCT FROM 'refund' THEN
        RAISE EXCEPTION 'a refunded delivery exception cannot change branch';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER delivery_exception_guard BEFORE UPDATE OR DELETE ON delivery_exceptions
FOR EACH ROW EXECUTE FUNCTION delivery_exception_guard();

ALTER TABLE order_refunds DROP CONSTRAINT IF EXISTS order_refunds_reason_code_check;
ALTER TABLE order_refunds ADD CONSTRAINT order_refunds_reason_code_check
    CHECK (reason_code IN ('return', 'dispute', 'late_payment', 'duplicate_payment', 'cancellation', 'delivery_exception'));

ALTER TABLE order_effects DROP CONSTRAINT IF EXISTS order_effects_kind_check;
ALTER TABLE order_effects ADD CONSTRAINT order_effects_kind_check CHECK (kind IN (
    'create_shipment', 'cancel_shipment', 'release_inventory', 'notify', 'request_refund', 'restock_return', 'settle_vendor_order',
    'report_rejected_outcome', 'acquire_settlement_hold', 'release_settlement_hold', 'stop_fulfillment', 'recover_cancelled_stock',
    'create_replacement_attempt', 'recover_delivery_stock'));

ALTER TABLE order_admin_audit DROP CONSTRAINT IF EXISTS order_admin_audit_entity_type_check;
ALTER TABLE order_admin_audit ADD CONSTRAINT order_admin_audit_entity_type_check
    CHECK (entity_type IN ('order', 'refund', 'return_request', 'effect', 'commission_rule', 'support_case', 'support_intake',
        'cancellation_request', 'delivery_exception'));
