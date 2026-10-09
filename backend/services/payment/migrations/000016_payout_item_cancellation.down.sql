-- Refuses once an item was cancelled: the old constraint cannot hold it.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM payout_items WHERE status = 'cancelled') THEN
        RAISE EXCEPTION 'cancelled payout items exist; refusing to drop the status';
    END IF;
END $$;
ALTER TABLE payout_items DROP CONSTRAINT IF EXISTS payout_items_status_check;
ALTER TABLE payout_items ADD CONSTRAINT payout_items_status_check CHECK (status IN ('pending', 'succeeded', 'failed'));
