-- ADM-01/04: admin actions in Shipment (shipping configuration and
-- fulfillment corrections) leave an append-only audit row written in the
-- same transaction as the change, with the request id that caused it.
-- Vendor actions stay on the shipment timeline (shipment_tracking_events).
CREATE TABLE shipment_admin_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id UUID NOT NULL,
    action TEXT NOT NULL CHECK (action ~ '^[a-z_]{1,60}$'),
    entity_type TEXT NOT NULL CHECK (entity_type IN ('shipment', 'carrier', 'zone', 'fee_rule', 'order_event')),
    entity_id TEXT NOT NULL CHECK (length(entity_id) BETWEEN 1 AND 100),
    reason TEXT CHECK (reason IS NULL OR length(reason) <= 1000),
    changes JSONB,
    request_id TEXT CHECK (request_id IS NULL OR length(request_id) <= 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX shipment_audit_recent_idx ON shipment_admin_audit (created_at DESC, id);
CREATE INDEX shipment_audit_actor_idx ON shipment_admin_audit (actor_id, created_at DESC);
CREATE INDEX shipment_audit_entity_idx ON shipment_admin_audit (entity_type, entity_id);
CREATE INDEX shipment_audit_request_idx ON shipment_admin_audit (request_id) WHERE request_id IS NOT NULL;

-- Backfill only fee rule versions, whose author was recorded. Admin
-- fulfillment actions before this version are on the shipment timeline.
INSERT INTO shipment_admin_audit (actor_id, action, entity_type, entity_id, changes, created_at)
SELECT created_by, 'fee_rule_set', 'fee_rule', id::text,
       jsonb_build_object('carrier_id', carrier_id, 'zone_id', zone_id, 'version', version, 'base_fee_amount', base_fee_amount,
                          'free_weight_grams', free_weight_grams, 'extra_fee_per_kg', extra_fee_per_kg), created_at
FROM shipping_fee_rules WHERE created_by IS NOT NULL;

CREATE FUNCTION shipment_audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Audit rows are append-only';
END $$;
CREATE TRIGGER shipment_audit_append_only BEFORE UPDATE OR DELETE ON shipment_admin_audit
FOR EACH ROW EXECUTE FUNCTION shipment_audit_append_only();
