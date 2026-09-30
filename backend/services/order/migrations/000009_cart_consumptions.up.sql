-- CRT-02: durable "consume the purchased cart lines" task, written in the
-- same transaction as the order. Replaces the best-effort clear of the
-- whole cart, which could delete lines the buyer added during checkout.
CREATE TABLE cart_consumptions (
    order_id UUID PRIMARY KEY REFERENCES orders (id),
    buyer_id UUID NOT NULL,
    operation_id UUID NOT NULL UNIQUE,
    lines JSONB NOT NULL CHECK (jsonb_typeof(lines) = 'array' AND jsonb_array_length(lines) > 0),
    status TEXT NOT NULL DEFAULT 'held'
        CHECK (status IN ('held', 'pending', 'consumed', 'cancelled', 'parked')),
    attempts INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((status = 'consumed') = (consumed_at IS NOT NULL))
);

CREATE INDEX cart_consumptions_due_idx ON cart_consumptions (next_attempt_at) WHERE status = 'pending';
CREATE INDEX cart_consumptions_held_idx ON cart_consumptions (created_at) WHERE status = 'held';
CREATE INDEX cart_consumptions_buyer_open_idx ON cart_consumptions (buyer_id) WHERE status IN ('held', 'pending');
