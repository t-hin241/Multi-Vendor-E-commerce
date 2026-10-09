-- Refuses once a version was recorded: it is audit context and is never
-- dropped silently.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM return_request_events WHERE membership_version IS NOT NULL) THEN
        RAISE EXCEPTION 'membership versions are recorded in return_request_events; refusing to drop them';
    END IF;
END $$;
ALTER TABLE return_request_events DROP COLUMN IF EXISTS membership_version;
