-- Refunds requested by Order: idempotent by Order's refund id, with the
-- currency, evidence and actor needed before money counts as returned.
ALTER TABLE payment_refunds
    ADD COLUMN currency TEXT,
    ADD COLUMN order_refund_id UUID,
    ADD COLUMN evidence_reference TEXT,
    ADD COLUMN resolved_by UUID,
    ADD COLUMN note TEXT;

UPDATE payment_refunds r SET currency = p.currency
FROM payment_intents p
WHERE p.id = r.payment_intent_id AND r.currency IS NULL;

ALTER TABLE payment_refunds
    ALTER COLUMN currency SET NOT NULL,
    ADD CONSTRAINT payment_refunds_currency_format CHECK (currency ~ '^[A-Z]{3}$'),
    -- Money counts as returned only with a provider or manual receipt.
    ADD CONSTRAINT payment_refunds_succeeded_needs_evidence CHECK (
        status <> 'succeeded' OR order_refund_id IS NULL
        OR evidence_reference IS NOT NULL OR provider_refund_id IS NOT NULL);

CREATE UNIQUE INDEX payment_refunds_order_refund_key ON payment_refunds (order_refund_id)
    WHERE order_refund_id IS NOT NULL;
CREATE INDEX payment_refunds_status_idx ON payment_refunds (status, created_at);
CREATE INDEX payment_refunds_intent_idx ON payment_refunds (payment_intent_id);

-- Durable delivery of a resolved refund's outcome back to Order.
CREATE TABLE payment_refund_sync (
    payment_refund_id UUID PRIMARY KEY REFERENCES payment_refunds (id),
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at TIMESTAMPTZ,
    requires_review BOOLEAN NOT NULL DEFAULT false,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX payment_refund_sync_due_idx ON payment_refund_sync (next_attempt_at)
    WHERE delivered_at IS NULL AND NOT requires_review;
