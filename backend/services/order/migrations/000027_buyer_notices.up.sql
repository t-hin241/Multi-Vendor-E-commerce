-- PW-009: notices to a buyer that have no order to ride on (support
-- requests without an order id: received, closed). Written in the
-- transaction that recorded the fact, relayed as order.notification_requested
-- (or over HTTP). References only: the request id, never its text.
CREATE TABLE order_buyer_notices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dedup_key TEXT NOT NULL UNIQUE CHECK (length(dedup_key) BETWEEN 1 AND 200),
    user_id UUID NOT NULL,
    notice_type TEXT NOT NULL CHECK (notice_type IN ('support_intake_received', 'support_intake_closed')),
    reference_id TEXT NOT NULL CHECK (reference_id ~ '^[A-Za-z0-9._:-]{1,100}$'),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT CHECK (last_error IS NULL OR length(last_error) <= 300),
    requires_review BOOLEAN NOT NULL DEFAULT false,
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX order_buyer_notices_due_idx ON order_buyer_notices (next_attempt_at)
    WHERE delivered_at IS NULL AND NOT requires_review;
