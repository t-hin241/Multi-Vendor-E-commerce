-- AF-08: a payout transfer's recorded result is told to the shop
-- (payment.vendor_action_required). The row is written in the transaction
-- that records the result and relayed to the event bus afterwards; it
-- carries references only (no amount, no destination).
CREATE TABLE payment_vendor_notices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id UUID NOT NULL,
    payout_item_id UUID NOT NULL REFERENCES payout_items(id) ON DELETE RESTRICT,
    outcome TEXT NOT NULL CHECK (outcome IN ('succeeded', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT CHECK (last_error IS NULL OR length(last_error) <= 300),
    requires_review BOOLEAN NOT NULL DEFAULT false,
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (payout_item_id, outcome)
);
CREATE INDEX payment_vendor_notices_due_idx ON payment_vendor_notices (next_attempt_at)
    WHERE delivered_at IS NULL AND NOT requires_review;
