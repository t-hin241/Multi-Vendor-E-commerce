DROP TABLE product_variant_options;
DROP TABLE product_variants;
ALTER TABLE attributes DROP CONSTRAINT attributes_variant_defining_select_only;
ALTER TABLE attributes DROP COLUMN is_variant_defining;
