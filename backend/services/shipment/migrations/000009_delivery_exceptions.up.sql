-- AF-04: failed delivery and returned goods. Shipment records the facts
-- (attempt limit reached, package back at the shop, package lost by the
-- carrier) and tells Order through the outbox; Order owns the resolution.
--
-- A vendor order may now have several fulfillment attempts: a redelivery
-- is a new shipment row (attempt_no + 1, original_shipment_id) that Order
-- asks for after the first one ended. The final row of an attempt is never
-- reset to shipped. At most one attempt per vendor order is active.

ALTER TABLE shipments DROP CONSTRAINT shipments_status_check;
ALTER TABLE shipments ADD CONSTRAINT shipments_status_check CHECK (status IN (
    'pending', 'ready_to_ship', 'shipped', 'delivered', 'cancelled', 'interception_requested', 'returned', 'lost'));

ALTER TABLE shipments
    ADD COLUMN attempt_no INTEGER NOT NULL DEFAULT 1 CHECK (attempt_no BETWEEN 1 AND 10),
    ADD COLUMN original_shipment_id UUID REFERENCES shipments (id),
    -- Order's operation id for a replacement attempt: a retry finds it.
    ADD COLUMN replacement_operation_id TEXT UNIQUE CHECK (replacement_operation_id IS NULL OR length(replacement_operation_id) BETWEEN 1 AND 100),
    ADD COLUMN lost_at TIMESTAMPTZ,
    ADD CONSTRAINT shipments_attempt_origin_check CHECK ((attempt_no = 1) = (original_shipment_id IS NULL));

DROP INDEX shipments_vendor_order_id_key;
CREATE UNIQUE INDEX shipments_vendor_order_attempt_key ON shipments (vendor_order_id, attempt_no);
CREATE UNIQUE INDEX shipments_vendor_order_active_key ON shipments (vendor_order_id)
    WHERE status IN ('pending', 'ready_to_ship', 'shipped', 'interception_requested');

-- Exception facts for Order, one per shipment and type, next to the status
-- facts (an old consumer never sees the new types: they use their own
-- event type and HTTP route).
ALTER TABLE shipment_outbox DROP CONSTRAINT shipment_outbox_event_type_check;
ALTER TABLE shipment_outbox ADD CONSTRAINT shipment_outbox_event_type_check CHECK (event_type IN (
    'shipped', 'delivered', 'returned', 'exception_attempts_exhausted', 'exception_returned', 'exception_lost'));
ALTER TABLE shipment_outbox
    ADD COLUMN attempt_no INTEGER,
    ADD COLUMN failed_attempts INTEGER,
    ADD COLUMN reason TEXT CHECK (reason IS NULL OR length(reason) <= 500);
