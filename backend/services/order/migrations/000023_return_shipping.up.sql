-- AF-05: the way back. An approved return gets a shipping authorization:
-- the shop's verified return destination (snapshot), who pays the return
-- fee, and a dispatch deadline. The buyer reports the carrier and tracking
-- number; Shipment carries the parcel (a return shipment Order drives);
-- the shop records what it received (every unit sellable, damaged or
-- missing). Only sellable units go back to stock; damaged or missing goods
-- wait for an admin instead of a silent deduction. A missed deadline puts
-- the return in review; it never takes the refund away by itself.

ALTER TABLE return_requests
    ADD COLUMN authorization_version INTEGER NOT NULL DEFAULT 0 CHECK (authorization_version >= 0),
    ADD COLUMN authorized_at TIMESTAMPTZ,
    ADD COLUMN authorized_by UUID,
    -- Shop's destination as verified when authorized: address, receiving
    -- hours, Vendor's address id and destination version.
    ADD COLUMN return_destination JSONB,
    ADD COLUMN return_fee_payer TEXT CHECK (return_fee_payer IN ('buyer', 'seller')),
    -- Approved reimbursement cap when the seller pays (paid through a
    -- separate disbursement, not here).
    ADD COLUMN return_fee_cap BIGINT CHECK (return_fee_cap IS NULL OR return_fee_cap >= 0),
    ADD COLUMN dispatch_deadline TIMESTAMPTZ,
    ADD COLUMN shipping_status TEXT CHECK (shipping_status IN ('destination_missing', 'awaiting_dispatch', 'awaiting_verification',
        'received', 'lost')),
    ADD COLUMN return_shipment_id UUID,
    ADD COLUMN dispatch_carrier TEXT CHECK (dispatch_carrier IS NULL OR length(dispatch_carrier) BETWEEN 1 AND 60),
    ADD COLUMN dispatch_tracking TEXT CHECK (dispatch_tracking IS NULL OR length(dispatch_tracking) BETWEEN 3 AND 64),
    ADD COLUMN dispatched_at TIMESTAMPTZ,
    ADD COLUMN dispatch_key TEXT CHECK (dispatch_key IS NULL OR dispatch_key ~ '^[A-Za-z0-9._:-]{8,100}$'),
    ADD COLUMN dispatch_hash TEXT,
    ADD COLUMN dispatch_overdue_at TIMESTAMPTZ,
    -- Units put back in stock (sellable ones); NULL keeps the old rule
    -- (the whole quantity when restock was chosen).
    ADD COLUMN restock_quantity BIGINT CHECK (restock_quantity IS NULL OR restock_quantity >= 0),
    ADD COLUMN inspection_disputed BOOLEAN NOT NULL DEFAULT false,
    ADD CONSTRAINT return_requests_authorization_check CHECK ((authorization_version = 0) = (return_destination IS NULL)),
    ADD CONSTRAINT return_requests_dispatch_check CHECK (
        shipping_status IS DISTINCT FROM 'awaiting_verification' OR (dispatch_tracking IS NOT NULL AND dispatched_at IS NOT NULL));
CREATE INDEX return_requests_dispatch_due_idx ON return_requests (dispatch_deadline, id)
    WHERE status = 'approved' AND shipping_status = 'awaiting_dispatch' AND dispatch_overdue_at IS NULL;

-- What the shop received for a return; append-only.
CREATE TABLE return_goods_receipts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    return_id UUID NOT NULL REFERENCES return_requests (id),
    receipt_version INTEGER NOT NULL CHECK (receipt_version >= 1),
    recorded_by UUID NOT NULL,
    actor_role TEXT NOT NULL CHECK (actor_role IN ('vendor', 'admin')),
    sellable_quantity BIGINT NOT NULL CHECK (sellable_quantity >= 0),
    damaged_quantity BIGINT NOT NULL CHECK (damaged_quantity >= 0),
    missing_quantity BIGINT NOT NULL CHECK (missing_quantity >= 0),
    note TEXT CHECK (note IS NULL OR length(note) <= 1000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (return_id, receipt_version)
);
CREATE TRIGGER return_goods_receipts_append_only BEFORE UPDATE OR DELETE ON return_goods_receipts
FOR EACH ROW EXECUTE FUNCTION support_append_only();

ALTER TABLE order_effects DROP CONSTRAINT IF EXISTS order_effects_kind_check;
ALTER TABLE order_effects ADD CONSTRAINT order_effects_kind_check CHECK (kind IN (
    'create_shipment', 'cancel_shipment', 'release_inventory', 'notify', 'request_refund', 'restock_return', 'settle_vendor_order',
    'report_rejected_outcome', 'acquire_settlement_hold', 'release_settlement_hold', 'stop_fulfillment', 'recover_cancelled_stock',
    'create_replacement_attempt', 'recover_delivery_stock', 'authorize_return_shipment', 'dispatch_return_shipment',
    'close_return_shipment'));
