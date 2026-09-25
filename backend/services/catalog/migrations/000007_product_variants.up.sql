ALTER TABLE attributes ADD COLUMN is_variant_defining BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE attributes ADD CONSTRAINT attributes_variant_defining_select_only
    CHECK (NOT is_variant_defining OR data_type = 'select');

CREATE TABLE product_variants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id UUID NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    sku TEXT NOT NULL,
    variant_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX product_variants_sku_key ON product_variants (sku);
CREATE UNIQUE INDEX product_variants_product_id_variant_key_key ON product_variants (product_id, variant_key);
CREATE INDEX product_variants_product_id_idx ON product_variants (product_id);

CREATE TABLE product_variant_options (
    variant_id UUID NOT NULL REFERENCES product_variants (id) ON DELETE CASCADE,
    attribute_id UUID NOT NULL REFERENCES attributes (id),
    option_id UUID NOT NULL REFERENCES attribute_options (id),
    PRIMARY KEY (variant_id, attribute_id)
);
