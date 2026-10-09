-- Refuse while return parcels exist: Order's returns point at them. Turn
-- FEATURE_RETURN_SHIPPING_ENABLED off instead of migrating down.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM return_shipments) THEN
        RAISE EXCEPTION 'return shipments exist; turn FEATURE_RETURN_SHIPPING_ENABLED off instead of migrating down';
    END IF;
END $$;

DROP TABLE return_shipments;
