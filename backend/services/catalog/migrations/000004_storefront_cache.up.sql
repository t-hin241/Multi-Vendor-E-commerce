CREATE TABLE vendor_name_cache (
    vendor_id UUID PRIMARY KEY,
    shop_name TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE product_sales_cache (
    product_id UUID PRIMARY KEY,
    quantity_sold BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
