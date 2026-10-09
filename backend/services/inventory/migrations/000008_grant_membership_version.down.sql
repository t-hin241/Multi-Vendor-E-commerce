-- Refuses once a version was recorded: it is audit context and is never
-- dropped silently.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM stock_movements WHERE membership_version IS NOT NULL) THEN
        RAISE EXCEPTION 'membership versions are recorded in stock_movements; refusing to drop them';
    END IF;
    IF EXISTS (SELECT 1 FROM inventory_operation_audit WHERE membership_version IS NOT NULL) THEN
        RAISE EXCEPTION 'membership versions are recorded in inventory_operation_audit; refusing to drop them';
    END IF;
END $$;
ALTER TABLE stock_movements DROP COLUMN IF EXISTS membership_version;
ALTER TABLE inventory_operation_audit DROP COLUMN IF EXISTS membership_version;
