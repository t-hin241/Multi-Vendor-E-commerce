-- Validates the per-line quantity limit added NOT VALID by Cart migration
-- 000004_cart_versioning. New writes are already enforced; this checks
-- legacy rows. Fails (and changes nothing) while any quantity > 999 exists:
-- review those lines with cart-preflight.sql first.
SET lock_timeout = '5s';
SET statement_timeout = '60s';
ALTER TABLE cart_items VALIDATE CONSTRAINT cart_items_quantity_limit;
