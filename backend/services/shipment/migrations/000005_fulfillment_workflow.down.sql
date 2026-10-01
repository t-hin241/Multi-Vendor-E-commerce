-- Loses the outbox and audit columns. Fails while a shipment is 'returned';
-- those must be reviewed first.
DROP TABLE IF EXISTS shipment_outbox;
DROP INDEX IF EXISTS shipment_tracking_events_key;
ALTER TABLE shipment_tracking_events
    DROP COLUMN IF EXISTS occurred_at,
    DROP COLUMN IF EXISTS event_key,
    DROP COLUMN IF EXISTS actor_role,
    DROP COLUMN IF EXISTS actor_id;
DROP INDEX IF EXISTS shipments_status_updated_idx;
ALTER TABLE shipments
    DROP COLUMN IF EXISTS address_redacted_at,
    DROP COLUMN IF EXISTS tracking_updated_at,
    DROP COLUMN IF EXISTS cancelled_at,
    DROP COLUMN IF EXISTS returned_at,
    DROP COLUMN IF EXISTS last_attempt_reason,
    DROP COLUMN IF EXISTS failed_attempts,
    DROP COLUMN IF EXISTS version;
ALTER TABLE shipments DROP CONSTRAINT shipments_status_check;
ALTER TABLE shipments ADD CONSTRAINT shipments_status_check CHECK (status IN (
    'pending', 'ready_to_ship', 'shipped', 'delivered', 'cancelled', 'interception_requested'));
