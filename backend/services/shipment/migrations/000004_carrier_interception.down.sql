ALTER TABLE shipments DROP CONSTRAINT shipments_status_check;
ALTER TABLE shipments ADD CONSTRAINT shipments_status_check
    CHECK (status IN ('pending', 'ready_to_ship', 'shipped', 'delivered', 'cancelled'));

DROP INDEX IF EXISTS shipments_intercept_provider_ref_key;
ALTER TABLE shipments
    DROP COLUMN intercept_provider_ref,
    DROP COLUMN intercept_requested_at,
    DROP COLUMN intercept_resolved_at;
