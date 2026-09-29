CREATE TABLE product_sale_status (
 product_id UUID PRIMARY KEY,
 is_visible BOOLEAN NOT NULL,
 version BIGINT NOT NULL CHECK(version > 0),
 confirmed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
