-- AF-05: the way back. A return shipment carries returned goods from the
-- buyer to the shop's verified return destination. Order owns the return
-- and drives every step (authorized → dispatched by the buyer → received
-- by the shop, or a transit exception); Shipment keeps the parcel's facts.
-- It never reuses an outbound shipment, needs no paid-order handover
-- grant, and holds no buyer address (only the shop's destination).

CREATE TABLE return_shipments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    return_id UUID NOT NULL,
    order_id UUID NOT NULL,
    vendor_id UUID NOT NULL,
    buyer_id UUID NOT NULL,
    -- Order's authorization this parcel follows; a destination corrected
    -- before dispatch raises it.
    authorization_version INTEGER NOT NULL CHECK (authorization_version >= 1),
    operation_id TEXT NOT NULL UNIQUE CHECK (length(operation_id) BETWEEN 1 AND 100),
    status TEXT NOT NULL CHECK (status IN ('pending_dispatch', 'in_transit', 'received', 'delivery_exception')),
    recipient_name TEXT NOT NULL,
    phone TEXT NOT NULL,
    province TEXT NOT NULL,
    district TEXT NOT NULL,
    ward TEXT NOT NULL DEFAULT '',
    street_address TEXT NOT NULL,
    receiving_hours TEXT NOT NULL,
    -- Entered by the buyer (manual tracking): a carrier may reuse numbers,
    -- so they are not unique.
    carrier_name TEXT CHECK (carrier_name IS NULL OR length(carrier_name) BETWEEN 1 AND 60),
    tracking_number TEXT CHECK (tracking_number IS NULL OR length(tracking_number) BETWEEN 3 AND 64),
    dispatched_at TIMESTAMPTZ,
    dispatch_operation_id TEXT,
    received_at TIMESTAMPTZ,
    exception_reason TEXT CHECK (exception_reason IS NULL OR length(exception_reason) <= 500),
    exception_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status <> 'in_transit' OR (tracking_number IS NOT NULL AND dispatched_at IS NOT NULL))
);
-- One active return parcel per return request.
CREATE UNIQUE INDEX return_shipments_active_idx ON return_shipments (return_id) WHERE status IN ('pending_dispatch', 'in_transit');
CREATE INDEX return_shipments_status_idx ON return_shipments (status, updated_at, id);
CREATE INDEX return_shipments_return_idx ON return_shipments (return_id);
