CREATE TABLE restock_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    inventory_item_id UUID NOT NULL REFERENCES inventory_items (id),
    product_id UUID NOT NULL,
    variant_id UUID,
    vendor_id UUID NOT NULL,
    requested_quantity BIGINT NOT NULL CHECK (requested_quantity > 0),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    requested_by UUID NOT NULL,
    rejection_reason TEXT,
    decided_by UUID,
    decided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX restock_requests_vendor_id_idx ON restock_requests (vendor_id);
CREATE INDEX restock_requests_status_idx ON restock_requests (status);
CREATE INDEX restock_requests_inventory_item_id_idx ON restock_requests (inventory_item_id);
