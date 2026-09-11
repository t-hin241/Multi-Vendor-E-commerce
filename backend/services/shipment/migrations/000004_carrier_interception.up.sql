-- Carrier interception: when a buyer cancels an order whose shipment has
-- already been handed to the carrier ("shipped"), Shipment asks the carrier
-- (today: a mock adapter standing in for a real one, see
-- internal/carrier/mock) whether it can still be pulled back. These columns
-- track that one-shot request/decision per shipment; intercept_provider_ref
-- is unique so the carrier's decision webhook can look the shipment back up,
-- and the conditional UPDATEs in the repository (WHERE status = ... AND
-- intercept_resolved_at IS NULL) are what make processing idempotent — no
-- separate events table is needed since there is ever only one decision.
ALTER TABLE shipments
    ADD COLUMN intercept_provider_ref TEXT,
    ADD COLUMN intercept_requested_at TIMESTAMPTZ,
    ADD COLUMN intercept_resolved_at TIMESTAMPTZ;

CREATE UNIQUE INDEX shipments_intercept_provider_ref_key
    ON shipments (intercept_provider_ref)
    WHERE intercept_provider_ref IS NOT NULL;

ALTER TABLE shipments DROP CONSTRAINT shipments_status_check;
ALTER TABLE shipments ADD CONSTRAINT shipments_status_check
    CHECK (status IN ('pending', 'ready_to_ship', 'shipped', 'delivered', 'cancelled', 'interception_requested'));
