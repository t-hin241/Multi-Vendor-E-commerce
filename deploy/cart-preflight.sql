-- Cart upgrade preflight (CRT-01..04). Read only; run against cart_db
-- before migration 000004_cart_versioning. Every violation count must be 0
-- or have a reviewed, approved fix before the migration runs.
SELECT 'duplicate_product_lines' AS check_name, count(*) AS violations FROM
  (SELECT cart_id, product_id FROM cart_items WHERE variant_id IS NULL GROUP BY cart_id, product_id HAVING count(*) > 1) d
UNION ALL SELECT 'duplicate_variant_lines', count(*) FROM
  (SELECT cart_id, variant_id FROM cart_items WHERE variant_id IS NOT NULL GROUP BY cart_id, variant_id HAVING count(*) > 1) d
UNION ALL SELECT 'non_positive_quantity', count(*) FROM cart_items WHERE quantity <= 0
UNION ALL SELECT 'orphan_items', count(*) FROM cart_items i LEFT JOIN carts c ON c.id = i.cart_id WHERE c.id IS NULL;

-- Informational: legacy data the new rules treat differently. Not blocking.
--  * quantity_over_limit: blocks cart-validate.sql until reviewed. The
--    buyer sees the line and cannot check out more than 999 per item.
--  * carts_over_line_limit: the buyer must remove lines before checkout.
--  * idle_carts_*: carts the retention worker deletes on its first run with
--    the default 180 days. Review before enabling CART_RETENTION_ENABLED.
SELECT 'quantity_over_limit' AS check_name, count(*) AS rows FROM cart_items WHERE quantity > 999
UNION ALL SELECT 'carts_over_line_limit', count(*) FROM
  (SELECT cart_id FROM cart_items GROUP BY cart_id HAVING count(*) > 50) d
UNION ALL SELECT 'idle_carts_180_days', count(*) FROM carts WHERE updated_at < now() - interval '180 days'
UNION ALL SELECT 'idle_cart_lines_180_days', count(*) FROM cart_items i JOIN carts c ON c.id = i.cart_id
  WHERE c.updated_at < now() - interval '180 days';
