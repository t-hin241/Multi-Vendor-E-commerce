-- SHP-02..05: compare-and-set state, manual tracking with audit, delivery
-- attempts and returns, a deduplicated event log, an outbox telling Order
-- about shipped/delivered/returned, and address retention.

ALTER TABLE shipments DROP CONSTRAINT shipments_status_check;
ALTER TABLE shipments ADD CONSTRAINT shipments_status_check CHECK (status IN (
    'pending', 'ready_to_ship', 'shipped', 'delivered', 'cancelled', 'interception_requested', 'returned'));

ALTER TABLE shipments
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN failed_attempts INTEGER NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
    ADD COLUMN last_attempt_reason TEXT CHECK (length(last_attempt_reason) <= 500),
    ADD COLUMN returned_at TIMESTAMPTZ,
    ADD COLUMN cancelled_at TIMESTAMPTZ,
    ADD COLUMN tracking_updated_at TIMESTAMPTZ,
    ADD COLUMN address_redacted_at TIMESTAMPTZ;
UPDATE shipments SET tracking_updated_at = COALESCE(shipped_at, updated_at) WHERE tracking_number IS NOT NULL;
UPDATE shipments SET cancelled_at = updated_at WHERE status = 'cancelled';
CREATE INDEX shipments_status_updated_idx ON shipments (status, updated_at);

-- Who did what: vendor, admin, carrier or the system. event_key makes a
-- carrier delivery or a retried action land once.
ALTER TABLE shipment_tracking_events
    ADD COLUMN actor_id UUID,
    ADD COLUMN actor_role TEXT NOT NULL DEFAULT 'system' CHECK (actor_role IN ('vendor', 'admin', 'carrier', 'system')),
    ADD COLUMN event_key TEXT,
    ADD COLUMN occurred_at TIMESTAMPTZ NOT NULL DEFAULT now();
UPDATE shipment_tracking_events SET occurred_at = created_at;
CREATE UNIQUE INDEX shipment_tracking_events_key ON shipment_tracking_events (shipment_id, event_key) WHERE event_key IS NOT NULL;

-- Outcomes Order must learn, written with the status change and retried
-- until Order acknowledges. One row per shipment and event type.
CREATE TABLE shipment_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shipment_id UUID NOT NULL REFERENCES shipments (id),
    vendor_order_id UUID NOT NULL,
    event_type TEXT NOT NULL CHECK (event_type IN ('shipped', 'delivered', 'returned')),
    occurred_at TIMESTAMPTZ NOT NULL,
    tracking_number TEXT,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at TIMESTAMPTZ,
    requires_review BOOLEAN NOT NULL DEFAULT false,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (shipment_id, event_type)
);
CREATE INDEX shipment_outbox_due_idx ON shipment_outbox (next_attempt_at) WHERE delivered_at IS NULL AND NOT requires_review;
