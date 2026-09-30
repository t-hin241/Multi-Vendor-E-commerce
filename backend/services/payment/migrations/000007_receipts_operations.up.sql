-- PAY-01/02/05: durable webhook receipts, intent operations persisted before
-- the provider call, and an audit trail for admin reconciliation actions.

-- One open (creating or pending) intent per order. Resolve duplicates found
-- by deploy/payment-preflight.sql first; this migration refuses to guess.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM payment_intents WHERE status = 'pending' GROUP BY order_id HAVING count(*) > 1) THEN
        RAISE EXCEPTION 'orders with several pending payment intents exist; resolve them first (deploy/payment-preflight.sql)';
    END IF;
END $$;

-- creating: persisted before the provider call, link not confirmed yet.
-- expired: the provider link closed (expired, cancelled, never created)
-- without a payment. A late capture on an expired or failed intent is still
-- recorded: money that arrived is never dropped.
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_status_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_status_check CHECK (
    status IN ('creating', 'pending', 'authorized', 'captured', 'failed', 'refunded', 'expired'));
ALTER TABLE payment_intents ALTER COLUMN provider_intent_id DROP NOT NULL;
ALTER TABLE payment_intents
    -- Our stable reference sent to the provider (payOS orderCode). Written
    -- before the call so a timeout can be resolved by querying it.
    ADD COLUMN provider_reference TEXT,
    ADD COLUMN create_attempts INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN last_error TEXT,
    ADD COLUMN last_checked_at TIMESTAMPTZ,
    ADD COLUMN closed_reason TEXT;
CREATE UNIQUE INDEX payment_intents_provider_reference_key ON payment_intents (provider, provider_reference)
    WHERE provider_reference IS NOT NULL;
CREATE UNIQUE INDEX payment_intents_open_order_key ON payment_intents (order_id)
    WHERE status IN ('creating', 'pending');
CREATE INDEX payment_intents_reconcile_idx ON payment_intents (updated_at)
    WHERE status IN ('creating', 'pending');
-- payOS orderCode: a positive integer unique per merchant, at most 2^53-1.
-- Starts above the millisecond timestamps the previous adapter used.
CREATE SEQUENCE payment_provider_order_code_seq START WITH 100000000000000;

-- Every verified webhook delivery (or reconciliation finding), recorded
-- before it is applied. Only 'processed' means its local effect committed.
CREATE TABLE payment_receipts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider TEXT NOT NULL,
    provider_event_id TEXT NOT NULL,
    provider_intent_id TEXT,
    provider_reference TEXT,
    payment_intent_id UUID REFERENCES payment_intents (id),
    event_type TEXT NOT NULL,
    amount BIGINT,
    currency TEXT,
    failure_reason TEXT,
    -- received: stored, not applied; processed: applied (or found to be a
    -- duplicate/stale event); retryable: applying failed, retried by the
    -- worker; rejected: verified but unusable (amount/currency mismatch);
    -- parked: no matching intent yet, retried then left for reconciliation.
    status TEXT NOT NULL DEFAULT 'received' CHECK (status IN ('received', 'processed', 'retryable', 'rejected', 'parked')),
    outcome TEXT,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ,
    UNIQUE (provider, provider_event_id)
);
CREATE INDEX payment_receipts_due_idx ON payment_receipts (next_attempt_at)
    WHERE status IN ('received', 'retryable', 'parked');
CREATE INDEX payment_receipts_intent_idx ON payment_receipts (payment_intent_id);
CREATE INDEX payment_receipts_status_idx ON payment_receipts (status, received_at);

-- Deliveries the previous version already applied stay deduplicated.
INSERT INTO payment_receipts (provider, provider_event_id, provider_intent_id, payment_intent_id, event_type,
                              status, outcome, received_at, processed_at)
SELECT p.provider, e.provider_event_id, p.provider_intent_id, e.payment_intent_id, e.event_type,
       'processed', 'legacy', e.created_at, e.created_at
FROM payment_events e JOIN payment_intents p ON p.id = e.payment_intent_id
ON CONFLICT (provider, provider_event_id) DO NOTHING;

-- Who retried or overrode what during reconciliation, and why.
CREATE TABLE payment_admin_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id UUID NOT NULL,
    action TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id TEXT NOT NULL,
    reason TEXT NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX payment_admin_audit_target_idx ON payment_admin_audit (target_type, target_id, created_at);
