-- Read-only AF-10 checks against catalog_db after migration 000017
-- (deploy/variant-pricing-runbook.md). Every row must be 0, except
-- differentiated_while_disabled once a pilot has priced SKUs apart: from
-- then on a rollback to old readers is no longer allowed.
BEGIN READ ONLY;
SET LOCAL statement_timeout = '60s';
SET LOCAL lock_timeout = '5s';
SELECT 'invalid_variant_prices' AS check_name, count(*) AS violations
FROM product_variants
WHERE price_amount IS NULL OR price_amount <= 0 OR price_version <= 0
UNION ALL
-- The current price of every SKU has its audit row (backfill or writer).
SELECT 'missing_price_audit', count(*)
FROM product_variants v
LEFT JOIN variant_price_audit a ON a.variant_id = v.id AND a.price_version = v.price_version
WHERE a.variant_id IS NULL
UNION ALL
SELECT 'audit_price_mismatch', count(*)
FROM product_variants v
JOIN variant_price_audit a ON a.variant_id = v.id AND a.price_version = v.price_version
WHERE a.new_price <> v.price_amount
UNION ALL
SELECT 'differentiated_while_disabled', count(*)
FROM product_variants v JOIN products p ON p.id = v.product_id
WHERE v.price_amount <> p.price_amount
UNION ALL
-- Parked events need an operator replay after the broker is back.
SELECT 'variant_price_outbox_parked', count(*) FROM variant_price_outbox WHERE attempts >= 5
UNION ALL
SELECT 'variant_price_outbox_stale', count(*)
FROM variant_price_outbox WHERE attempts < 5 AND created_at < now() - interval '5 minutes'
UNION ALL
SELECT 'missing_price_triggers', 3 - count(*)
FROM pg_trigger
WHERE NOT tgisinternal AND tgname IN
  ('catalog_variant_price_fence', 'catalog_variant_price_defaults', 'variant_price_audit_immutable');
COMMIT;
