CREATE TABLE return_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
    order_item_id UUID NOT NULL REFERENCES order_items(id) ON DELETE RESTRICT,
    buyer_id UUID NOT NULL,
    reason TEXT NOT NULL CHECK (length(reason) BETWEEN 1 AND 2000),
    status TEXT NOT NULL DEFAULT 'requested' CHECK (status IN ('requested', 'vendor_confirmed', 'rejected', 'approved_awaiting_provider_refund', 'refunded')),
    vendor_confirmed_by UUID,
    vendor_confirmed_at TIMESTAMPTZ,
    decided_by UUID,
    decision_note TEXT,
    decided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(order_item_id)
);
CREATE INDEX return_requests_buyer_idx ON return_requests(buyer_id, created_at DESC);
CREATE INDEX return_requests_status_idx ON return_requests(status, created_at DESC);
