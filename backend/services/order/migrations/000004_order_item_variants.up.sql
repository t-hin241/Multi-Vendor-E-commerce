ALTER TABLE order_items ADD COLUMN variant_id UUID;
ALTER TABLE order_items ADD COLUMN variant_sku TEXT;
ALTER TABLE order_items ADD COLUMN variant_label TEXT;
