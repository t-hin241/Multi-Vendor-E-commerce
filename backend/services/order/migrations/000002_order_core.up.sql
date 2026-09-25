CREATE TABLE orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    buyer_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending_payment'
        CHECK (status IN ('pending_payment', 'paid', 'processing', 'shipped', 'completed', 'cancelled', 'refunded')),
    total_amount BIGINT NOT NULL CHECK (total_amount > 0),
    currency TEXT NOT NULL,
    cancellation_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX orders_buyer_id_idx ON orders (buyer_id);
CREATE INDEX orders_status_idx ON orders (status);

CREATE TABLE vendor_orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    vendor_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending_payment'
        CHECK (status IN ('pending_payment', 'paid', 'processing', 'shipped', 'completed', 'cancelled', 'refunded')),
    subtotal_amount BIGINT NOT NULL CHECK (subtotal_amount > 0),
    currency TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX vendor_orders_order_id_idx ON vendor_orders (order_id);
CREATE INDEX vendor_orders_vendor_id_idx ON vendor_orders (vendor_id);

CREATE TABLE order_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    vendor_order_id UUID NOT NULL REFERENCES vendor_orders (id) ON DELETE CASCADE,
    product_id UUID NOT NULL,
    product_name TEXT NOT NULL,
    price_amount BIGINT NOT NULL CHECK (price_amount > 0),
    quantity BIGINT NOT NULL CHECK (quantity > 0),
    subtotal_amount BIGINT NOT NULL CHECK (subtotal_amount > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX order_items_order_id_idx ON order_items (order_id);
CREATE INDEX order_items_vendor_order_id_idx ON order_items (vendor_order_id);
