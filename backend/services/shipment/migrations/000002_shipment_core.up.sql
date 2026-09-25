CREATE TABLE shipments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_order_id UUID NOT NULL,
    vendor_id UUID NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'ready_to_ship', 'shipped', 'delivered')),
    carrier TEXT,
    tracking_number TEXT,
    shipped_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One shipment per vendor sub-order.
CREATE UNIQUE INDEX shipments_vendor_order_id_key ON shipments (vendor_order_id);
CREATE INDEX shipments_vendor_id_idx ON shipments (vendor_id);
