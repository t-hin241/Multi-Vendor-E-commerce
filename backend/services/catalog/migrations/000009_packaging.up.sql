-- Four reserved attributes back packaging's per-category required/optional
-- rule, reusing the existing Attribute Management System unchanged: admin
-- authors required/excluded per category through the same
-- category_attribute_rules mechanism every other attribute already uses.
-- Their codes are reserved — the catalog usecase recognizes them by code
-- and routes their submitted values into product_packaging below instead
-- of the generic product_attribute_values table.
INSERT INTO attributes (code, name, data_type, unit, is_variant_defining) VALUES
    ('pkg_weight', 'Package weight', 'number', 'g', FALSE),
    ('pkg_length', 'Package length', 'number', 'mm', FALSE),
    ('pkg_width', 'Package width', 'number', 'mm', FALSE),
    ('pkg_height', 'Package height', 'number', 'mm', FALSE);

-- One row per product (packaging is product-level only in this pass, not
-- per-variant) — a dedicated table rather than product_attribute_values,
-- since the latter is one-arbitrary-value-per-row and awkward for 4 fixed
-- numeric columns that Shipment needs to read reliably for fee
-- calculation.
CREATE TABLE product_packaging (
    product_id UUID PRIMARY KEY REFERENCES products (id) ON DELETE CASCADE,
    weight_grams BIGINT,
    length_mm BIGINT,
    width_mm BIGINT,
    height_mm BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
