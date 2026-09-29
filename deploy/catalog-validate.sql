-- Run after reviewing preflight results. Fails without modifying legacy data if violations remain.
BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='60s';
ALTER TABLE product_variant_options VALIDATE CONSTRAINT variant_option_attribute_fk;
ALTER TABLE product_attribute_values VALIDATE CONSTRAINT value_option_attribute_fk;
ALTER TABLE product_attribute_values VALIDATE CONSTRAINT value_exactly_one;
ALTER TABLE product_variants VALIDATE CONSTRAINT variant_sku_nonempty;
COMMIT;
