-- Validates the overflow guard added NOT VALID by Inventory migration
-- 000005_reservation_operations. New writes are already enforced; this
-- checks legacy rows. It fails (and changes nothing) if any item has
-- available + reserved above the BIGINT range: review those with
-- inventory-preflight.sql ('quantity_overflow') first.
SET lock_timeout = '5s';
SET statement_timeout = '60s';
ALTER TABLE inventory_items VALIDATE CONSTRAINT inventory_total_quantity_safe;
