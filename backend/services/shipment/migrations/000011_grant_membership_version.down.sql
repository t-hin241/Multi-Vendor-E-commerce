-- Refuses once a version was recorded: it is audit context and is never
-- dropped silently.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM shipment_tracking_events WHERE membership_version IS NOT NULL) THEN
        RAISE EXCEPTION 'membership versions are recorded in shipment_tracking_events; refusing to drop them';
    END IF;
END $$;
ALTER TABLE shipment_tracking_events DROP COLUMN IF EXISTS membership_version;
