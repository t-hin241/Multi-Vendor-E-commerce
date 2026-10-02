-- Read-only data preflight: catalog_db (data tooling plan 14, DEV-04).
SELECT 'blocking'::text AS severity, 'migration_ledger_dirty'::text AS check_name, count(*)::bigint AS row_count,
       'a migration stopped half-way; resolve before anything else'::text AS meaning
  FROM schema_migrations WHERE dirty
UNION ALL
SELECT 'blocking', 'duplicate_sku', coalesce(sum(n - 1), 0),
       'variants sharing a SKU once case and spaces are ignored'
  FROM (SELECT count(*) AS n FROM product_variants GROUP BY lower(btrim(sku)) HAVING count(*) > 1) d
UNION ALL
SELECT 'blocking', 'duplicate_slug', coalesce(sum(n - 1), 0),
       'products sharing a slug'
  FROM (SELECT count(*) AS n FROM products GROUP BY lower(slug) HAVING count(*) > 1) d
UNION ALL
SELECT 'blocking', 'duplicate_variant_key', coalesce(sum(n - 1), 0),
       'variants of one product with the same option combination'
  FROM (SELECT count(*) AS n FROM product_variants GROUP BY product_id, variant_key HAVING count(*) > 1) d
UNION ALL
SELECT 'blocking', 'non_positive_price', count(*),
       'products priced at zero or less'
  FROM products WHERE price_amount <= 0
UNION ALL
SELECT 'warning', 'on_sale_from_shop_not_approved', count(*),
       'approved, active products of a shop whose last known status is not approved'
  FROM products p JOIN vendor_sale_status s ON s.vendor_id = p.vendor_id
 WHERE p.status = 'approved' AND p.is_active AND s.status <> 'approved'
UNION ALL
SELECT 'warning', 'events_parked', count(*),
       'events Catalog could not apply (see /admin/events)'
  FROM event_inbox WHERE status = 'parked'
UNION ALL
SELECT 'seed', 'hotlinked_product_media', count(*),
       'product images/videos served from an external CDN (scraper seed)'
  FROM (SELECT object_key FROM product_media UNION ALL SELECT object_key FROM product_images) m
 WHERE object_key LIKE 'external/%';
