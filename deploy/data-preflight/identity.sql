-- Read-only data preflight: identity_db (data tooling plan 14, DEV-04).
-- Report only; fixes go through a reviewed operation or migration.
SELECT 'blocking'::text AS severity, 'migration_ledger_dirty'::text AS check_name, count(*)::bigint AS row_count,
       'a migration stopped half-way; resolve before anything else'::text AS meaning
  FROM schema_migrations WHERE dirty
UNION ALL
SELECT 'blocking', 'duplicate_email', coalesce(sum(n - 1), 0),
       'accounts sharing an email once case and spaces are ignored'
  FROM (SELECT count(*) AS n FROM users GROUP BY lower(btrim(email)) HAVING count(*) > 1) d
UNION ALL
SELECT 'seed', 'seed_accounts', count(*),
       'fabricated scraper accounts (@scraped.local); none may exist in production'
  FROM users WHERE email LIKE '%@scraped.local'
UNION ALL
SELECT 'warning', 'no_active_admin', CASE WHEN count(*) = 0 THEN 1 ELSE 0 END,
       'nobody can operate the marketplace (bootstrap an admin)'
  FROM users WHERE role = 'admin' AND is_active;
