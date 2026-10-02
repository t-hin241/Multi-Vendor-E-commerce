-- Read-only data preflight: inventory_db (data tooling plan 14, DEV-04).
SELECT 'blocking'::text AS severity, 'migration_ledger_dirty'::text AS check_name, count(*)::bigint AS row_count,
       'a migration stopped half-way; resolve before anything else'::text AS meaning
  FROM schema_migrations WHERE dirty
UNION ALL
SELECT 'blocking', 'negative_stock', count(*),
       'stock items with negative available or reserved quantity'
  FROM inventory_items WHERE available_quantity < 0 OR reserved_quantity < 0
UNION ALL
SELECT 'blocking', 'duplicate_stock_item', coalesce(sum(n - 1), 0),
       'more than one stock item for the same product/variant'
  FROM (SELECT count(*) AS n FROM inventory_items GROUP BY product_id, variant_id HAVING count(*) > 1) d
UNION ALL
SELECT 'warning', 'reserved_differs_from_active_holds', count(*),
       'reserved quantity not equal to the sum of the item''s active holds'
  FROM inventory_items i
 WHERE i.reserved_quantity <> coalesce((SELECT sum(r.quantity) FROM stock_reservations r
                                          WHERE r.inventory_item_id = i.id AND r.status = 'active'), 0)
UNION ALL
SELECT 'warning', 'stale_holds', count(*),
       'holds still active more than 15 minutes after they expired (expiry worker behind or off)'
  FROM stock_reservations WHERE status = 'active' AND expires_at < now() - interval '15 minutes';
