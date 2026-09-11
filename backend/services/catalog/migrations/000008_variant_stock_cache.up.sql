CREATE TABLE variant_stock_cache (
    variant_id UUID PRIMARY KEY,
    available_quantity BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
