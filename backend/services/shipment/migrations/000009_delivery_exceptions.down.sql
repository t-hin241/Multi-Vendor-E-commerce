-- Refuse while AF-04 data exists: a second attempt, a lost package or an
-- exception fact Order may still need. Turn FEATURE_DELIVERY_RESOLUTION_ENABLED
-- off instead of migrating down.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM shipments WHERE attempt_no > 1 OR status = 'lost')
       OR EXISTS (SELECT 1 FROM shipment_outbox WHERE event_type LIKE 'exception_%') THEN
        RAISE EXCEPTION 'delivery exception data exists; turn FEATURE_DELIVERY_RESOLUTION_ENABLED off instead of migrating down';
    END IF;
END $$;

ALTER TABLE shipment_outbox DROP COLUMN reason, DROP COLUMN failed_attempts, DROP COLUMN attempt_no;
ALTER TABLE shipment_outbox DROP CONSTRAINT shipment_outbox_event_type_check;
ALTER TABLE shipment_outbox ADD CONSTRAINT shipment_outbox_event_type_check CHECK (event_type IN ('shipped', 'delivered', 'returned'));

DROP INDEX shipments_vendor_order_active_key;
DROP INDEX shipments_vendor_order_attempt_key;
CREATE UNIQUE INDEX shipments_vendor_order_id_key ON shipments (vendor_order_id);

ALTER TABLE shipments DROP CONSTRAINT shipments_attempt_origin_check;
ALTER TABLE shipments DROP COLUMN lost_at, DROP COLUMN replacement_operation_id, DROP COLUMN original_shipment_id, DROP COLUMN attempt_no;
ALTER TABLE shipments DROP CONSTRAINT shipments_status_check;
ALTER TABLE shipments ADD CONSTRAINT shipments_status_check CHECK (status IN (
    'pending', 'ready_to_ship', 'shipped', 'delivered', 'cancelled', 'interception_requested', 'returned'));
