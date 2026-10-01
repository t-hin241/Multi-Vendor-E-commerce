-- ADM-01/02/04: every admin action in Order leaves an append-only audit row
-- written in the same transaction as the change, with the request id that
-- caused it; an admin refund request carries an idempotency key so a resend
-- after a timeout cannot create a second refund.
CREATE TABLE order_admin_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id UUID NOT NULL,
    action TEXT NOT NULL CHECK (action ~ '^[a-z_]{1,60}$'),
    entity_type TEXT NOT NULL CHECK (entity_type IN ('order', 'refund', 'return_request', 'effect', 'commission_rule')),
    entity_id TEXT NOT NULL CHECK (length(entity_id) BETWEEN 1 AND 100),
    order_id UUID,
    reason TEXT CHECK (reason IS NULL OR length(reason) <= 1000),
    -- Only the fields the action changed, e.g. {"status": ["pending", "cancelled"]}.
    changes JSONB,
    request_id TEXT CHECK (request_id IS NULL OR length(request_id) <= 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX order_audit_recent_idx ON order_admin_audit (created_at DESC, id);
CREATE INDEX order_audit_actor_idx ON order_admin_audit (actor_id, created_at DESC);
CREATE INDEX order_audit_entity_idx ON order_admin_audit (entity_type, entity_id);
CREATE INDEX order_audit_order_idx ON order_admin_audit (order_id) WHERE order_id IS NOT NULL;
CREATE INDEX order_audit_request_idx ON order_admin_audit (request_id) WHERE request_id IS NOT NULL;

-- Backfill only what Order already recorded with its actor: commission rule
-- versions set by an admin and refunds an admin requested. Nothing else is
-- reconstructed, and no actor is invented for older history.
INSERT INTO order_admin_audit (actor_id, action, entity_type, entity_id, changes, created_at)
SELECT created_by, 'commission_rule_set', 'commission_rule', id::text, jsonb_build_object('rate_bps', rate_bps), created_at
FROM commission_rules WHERE created_by IS NOT NULL;
INSERT INTO order_admin_audit (actor_id, action, entity_type, entity_id, order_id, reason, changes, created_at)
SELECT requested_by, 'refund_requested', 'refund', id::text, order_id, reason,
       jsonb_build_object('amount', amount, 'currency', currency, 'reason_code', reason_code), created_at
FROM order_refunds WHERE reason_code IN ('dispute', 'late_payment', 'duplicate_payment');

CREATE FUNCTION order_audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Audit rows are append-only';
END $$;
CREATE TRIGGER order_audit_append_only BEFORE UPDATE OR DELETE ON order_admin_audit
FOR EACH ROW EXECUTE FUNCTION order_audit_append_only();

ALTER TABLE order_refunds ADD COLUMN idempotency_key TEXT
    CHECK (idempotency_key IS NULL OR idempotency_key ~ '^[A-Za-z0-9._:-]{8,100}$');
CREATE UNIQUE INDEX order_refunds_idempotency_key ON order_refunds (order_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
