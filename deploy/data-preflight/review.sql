-- Read-only data preflight: review_db (data tooling plan 14, DEV-04).
SELECT 'blocking'::text AS severity, 'migration_ledger_dirty'::text AS check_name, count(*)::bigint AS row_count,
       'a migration stopped half-way; resolve before anything else'::text AS meaning
  FROM schema_migrations WHERE dirty
UNION ALL
SELECT 'blocking', 'duplicate_review_per_item', coalesce(sum(n - 1), 0),
       'several reviews of the same purchased item'
  FROM (SELECT count(*) AS n FROM reviews WHERE order_item_id IS NOT NULL GROUP BY order_item_id HAVING count(*) > 1) d
UNION ALL
SELECT 'blocking', 'verified_without_purchase', count(*),
       'reviews marked as a verified purchase without the purchased item'
  FROM reviews WHERE verified_purchase AND (order_item_id IS NULL OR vendor_order_id IS NULL)
UNION ALL
SELECT 'seed', 'hotlinked_review_images', count(*),
       'review photos served from an external CDN (imported seed reviews)'
  FROM review_images WHERE object_key LIKE 'external/%'
UNION ALL
SELECT 'seed', 'unverified_reviews', count(*),
       'published reviews that are not verified purchases (imported seed reviews; hidden unless REVIEW_SHOW_UNVERIFIED)'
  FROM reviews WHERE status = 'published' AND NOT verified_purchase;
