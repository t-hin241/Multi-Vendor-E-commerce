-- Refuses once a version was recorded: it is audit context and is never
-- dropped silently.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM product_audit_logs WHERE membership_version IS NOT NULL) THEN
        RAISE EXCEPTION 'membership versions are recorded in product_audit_logs; refusing to drop them';
    END IF;
END $$;
ALTER TABLE product_audit_logs DROP COLUMN IF EXISTS membership_version;
