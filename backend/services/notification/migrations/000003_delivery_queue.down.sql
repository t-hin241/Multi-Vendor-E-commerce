-- Refuses while notifications are still queued or audited by this version:
-- rolling back must not drop pending deliveries or audit history. Pause the
-- worker and let the queue drain (or resolve parked rows) first.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM notifications WHERE status IN ('pending', 'sending', 'parked')) THEN
  RAISE EXCEPTION 'notifications are still queued or parked; keep migration 000003';
 END IF;
 IF EXISTS (SELECT 1 FROM notification_admin_audit) THEN
  RAISE EXCEPTION 'notification_admin_audit holds audit; keep migration 000003';
 END IF;
END $$;
DROP TABLE IF EXISTS notification_admin_audit;
DROP FUNCTION IF EXISTS notification_audit_append_only();
DROP TABLE IF EXISTS notification_attempts;
DROP INDEX IF EXISTS notifications_status_idx;
DROP INDEX IF EXISTS notifications_due_idx;
DROP INDEX IF EXISTS notifications_dedup_key;
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_fail_reason_length;
ALTER TABLE notifications
    DROP COLUMN IF EXISTS updated_at, DROP COLUMN IF EXISTS sent_at, DROP COLUMN IF EXISTS lease_until,
    DROP COLUMN IF EXISTS next_attempt_at, DROP COLUMN IF EXISTS max_attempts, DROP COLUMN IF EXISTS attempts,
    DROP COLUMN IF EXISTS recipient_masked, DROP COLUMN IF EXISTS correlation_id, DROP COLUMN IF EXISTS template_version,
    DROP COLUMN IF EXISTS dedup_key, DROP COLUMN IF EXISTS source, DROP COLUMN IF EXISTS event_id;
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_status_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_status_check CHECK (status IN ('sent', 'failed'));
