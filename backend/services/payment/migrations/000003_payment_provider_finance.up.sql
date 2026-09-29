ALTER TABLE payment_intents
    ADD COLUMN checkout_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN qr_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN expires_at TIMESTAMPTZ;

CREATE TABLE payment_refunds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_intent_id UUID NOT NULL REFERENCES payment_intents(id) ON DELETE RESTRICT,
    order_id UUID NOT NULL,
    amount BIGINT NOT NULL CHECK (amount > 0),
    reason TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('awaiting_provider_refund', 'pending', 'succeeded', 'failed')),
    provider_refund_id TEXT UNIQUE,
    requested_by UUID NOT NULL,
    resolved_at TIMESTAMPTZ,
    failure_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX payment_refunds_order_idx ON payment_refunds(order_id, created_at DESC);

CREATE TABLE payout_batches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider TEXT NOT NULL,
    provider_batch_id TEXT UNIQUE,
    idempotency_key TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('pending', 'submitted', 'succeeded', 'failed')),
    created_by UUID NOT NULL,
    failure_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE payout_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payout_batch_id UUID NOT NULL REFERENCES payout_batches(id) ON DELETE RESTRICT,
    vendor_id UUID NOT NULL,
    vendor_order_id UUID NOT NULL UNIQUE,
    amount BIGINT NOT NULL CHECK (amount > 0),
    destination_mask TEXT NOT NULL,
    provider_payout_id TEXT UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed')),
    failure_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX payout_items_vendor_idx ON payout_items(vendor_id, created_at DESC);
