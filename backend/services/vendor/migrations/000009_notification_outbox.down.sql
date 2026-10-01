-- Refuses while notices are still waiting, so a rollback does not drop
-- them; let the worker deliver (or resolve parked rows) first.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM vendor_notification_outbox WHERE delivered_at IS NULL) THEN
  RAISE EXCEPTION 'vendor notices are still queued; keep migration 000009';
 END IF;
END $$;
DROP TABLE IF EXISTS vendor_notification_outbox;
