-- PW-009: refund facts the buyer has to act on (AF-06): the refund needs a
-- destination account, or the one given was rejected. Written in the
-- transaction that decided the fact (or by the sweep for refunds without a
-- destination), relayed as payment.notification_requested. References
-- only: the order id, never an account or an amount.
CREATE TABLE payment_buyer_notices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dedup_key TEXT NOT NULL UNIQUE CHECK (length(dedup_key) BETWEEN 1 AND 200),
    user_id UUID NOT NULL,
    notice_type TEXT NOT NULL CHECK (notice_type IN ('refund_destination_needed', 'refund_destination_rejected')),
    reference_id TEXT NOT NULL CHECK (reference_id ~ '^[A-Za-z0-9._:-]{1,100}$'),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT CHECK (last_error IS NULL OR length(last_error) <= 300),
    requires_review BOOLEAN NOT NULL DEFAULT false,
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX payment_buyer_notices_due_idx ON payment_buyer_notices (next_attempt_at)
    WHERE delivered_at IS NULL AND NOT requires_review;
