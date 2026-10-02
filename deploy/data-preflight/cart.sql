-- Read-only data preflight: cart_db (data tooling plan 14, DEV-04).
SELECT 'blocking'::text AS severity, 'migration_ledger_dirty'::text AS check_name, count(*)::bigint AS row_count,
       'a migration stopped half-way; resolve before anything else'::text AS meaning
  FROM schema_migrations WHERE dirty
UNION ALL
SELECT 'blocking', 'non_positive_quantity', count(*),
       'cart lines with a quantity of zero or less'
  FROM cart_items WHERE quantity <= 0
UNION ALL
SELECT 'blocking', 'duplicate_cart_line', coalesce(sum(n - 1), 0),
       'the same product/variant twice in one cart'
  FROM (SELECT count(*) AS n FROM cart_items GROUP BY cart_id, product_id, variant_id HAVING count(*) > 1) d;
