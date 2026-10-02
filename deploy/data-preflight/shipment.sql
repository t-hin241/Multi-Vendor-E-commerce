-- Read-only data preflight: shipment_db (data tooling plan 14, DEV-04).
SELECT 'blocking'::text AS severity, 'migration_ledger_dirty'::text AS check_name, count(*)::bigint AS row_count,
       'a migration stopped half-way; resolve before anything else'::text AS meaning
  FROM schema_migrations WHERE dirty
UNION ALL
SELECT 'blocking', 'several_live_shipments', coalesce(sum(n - 1), 0),
       'vendor orders with more than one shipment that is not cancelled'
  FROM (SELECT count(*) AS n FROM shipments WHERE status <> 'cancelled' GROUP BY vendor_order_id HAVING count(*) > 1) d
UNION ALL
SELECT 'blocking', 'negative_fee', count(*),
       'shipments with a negative fee'
  FROM shipments WHERE fee_amount < 0
UNION ALL
SELECT 'warning', 'shipped_without_tracking', count(*),
       'shipped or delivered parcels with no tracking number'
  FROM shipments WHERE status IN ('shipped', 'delivered') AND coalesce(btrim(tracking_number), '') = ''
UNION ALL
SELECT 'warning', 'events_parked', count(*),
       'events Shipment could not apply (see /admin/events)'
  FROM event_inbox WHERE status = 'parked';
