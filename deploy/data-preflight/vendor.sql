-- Read-only data preflight: vendor_db (data tooling plan 14, DEV-04).
SELECT 'blocking'::text AS severity, 'migration_ledger_dirty'::text AS check_name, count(*)::bigint AS row_count,
       'a migration stopped half-way; resolve before anything else'::text AS meaning
  FROM schema_migrations WHERE dirty
UNION ALL
SELECT 'blocking', 'approved_without_default_pickup_address', count(*),
       'approved shops that cannot be shipped from'
  FROM vendors v WHERE v.status = 'approved'
   AND NOT EXISTS (SELECT 1 FROM vendor_addresses a WHERE a.vendor_id = v.id AND a.is_default)
UNION ALL
SELECT 'blocking', 'several_default_addresses', count(*),
       'shops with more than one default pickup address'
  FROM (SELECT vendor_id FROM vendor_addresses WHERE is_default GROUP BY vendor_id HAVING count(*) > 1) d
UNION ALL
SELECT 'warning', 'status_change_not_propagated', count(*),
       'shop status changed more than an hour ago and Catalog/Order have not applied it'
  FROM vendors WHERE version > enforced_version AND updated_at < now() - interval '1 hour'
UNION ALL
SELECT 'seed', 'fabricated_contacts', count(*),
       'placeholder phone/address from the scraper seed'
  FROM vendor_addresses WHERE phone = '0000000000' OR province LIKE 'N/A (fabricated%' OR street_address LIKE 'N/A (fabricated%'
UNION ALL
SELECT 'seed', 'hotlinked_branding', count(*),
       'shop logo/banner pointing at an external CDN instead of the media store'
  FROM vendors WHERE logo_object_key LIKE 'external/%' OR banner_object_key LIKE 'external/%';
