-- Read-only data preflight: notification_db (data tooling plan 14, DEV-04).
SELECT 'blocking'::text AS severity, 'migration_ledger_dirty'::text AS check_name, count(*)::bigint AS row_count,
       'a migration stopped half-way; resolve before anything else'::text AS meaning
  FROM schema_migrations WHERE dirty
UNION ALL
SELECT 'warning', 'notifications_parked', count(*),
       'notifications that gave up (see /admin/notifications)'
  FROM notifications WHERE status IN ('parked', 'failed')
UNION ALL
SELECT 'warning', 'stuck_sending', count(*),
       'notifications still sending well after their lease ended'
  FROM notifications WHERE status = 'sending' AND lease_until < now() - interval '15 minutes'
UNION ALL
SELECT 'warning', 'events_parked', count(*),
       'events Notification could not apply (see /admin/events)'
  FROM event_inbox WHERE status = 'parked';
