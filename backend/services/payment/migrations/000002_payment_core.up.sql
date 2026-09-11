CREATE TABLE payment_intents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL,
    buyer_id UUID NOT NULL,
    amount BIGINT NOT NULL CHECK (amount > 0),
    currency TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'authorized', 'captured', 'failed', 'refunded')),
    provider TEXT NOT NULL,
    provider_intent_id TEXT NOT NULL,
    failure_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX payment_intents_order_id_idx ON payment_intents (order_id);
CREATE UNIQUE INDEX payment_intents_provider_intent_id_key ON payment_intents (provider_intent_id);

-- One row per processed webhook delivery, keyed by the provider's own event
-- id. This is the actual idempotency guarantee: a retried delivery hits the
-- unique index and is treated as already handled, never reprocessed.
CREATE TABLE payment_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_event_id TEXT NOT NULL,
    payment_intent_id UUID NOT NULL REFERENCES payment_intents (id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX payment_events_provider_event_id_key ON payment_events (provider_event_id);
