CREATE TABLE inventory_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id UUID NOT NULL,
    vendor_id UUID NOT NULL,
    available_quantity BIGINT NOT NULL DEFAULT 0 CHECK (available_quantity >= 0),
    reserved_quantity BIGINT NOT NULL DEFAULT 0 CHECK (reserved_quantity >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX inventory_items_product_id_key ON inventory_items (product_id);
CREATE INDEX inventory_items_vendor_id_idx ON inventory_items (vendor_id);

CREATE TABLE stock_reservations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    inventory_item_id UUID NOT NULL REFERENCES inventory_items (id),
    order_id UUID NOT NULL,
    quantity BIGINT NOT NULL CHECK (quantity > 0),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'released', 'committed')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX stock_reservations_order_id_idx ON stock_reservations (order_id);
CREATE INDEX stock_reservations_inventory_item_id_idx ON stock_reservations (inventory_item_id);
CREATE INDEX stock_reservations_active_idx ON stock_reservations (status) WHERE status = 'active';

CREATE TABLE stock_movements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    inventory_item_id UUID NOT NULL REFERENCES inventory_items (id),
    change_quantity BIGINT NOT NULL,
    reason TEXT NOT NULL,
    reference_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX stock_movements_inventory_item_id_idx ON stock_movements (inventory_item_id);
