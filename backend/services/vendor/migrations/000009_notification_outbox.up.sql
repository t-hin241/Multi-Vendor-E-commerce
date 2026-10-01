-- NTF-01: a shop decision's notice to the owner is queued in the same
-- transaction as the decision and handed to Notification by a worker, so
-- a Notification outage no longer loses it. One notice per shop version
-- and type; Notification deduplicates again by the row id.
CREATE TABLE vendor_notification_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id UUID NOT NULL REFERENCES vendors (id),
    user_id UUID NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('vendor_approved', 'vendor_rejected')),
    version BIGINT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_until TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    -- Notification refused the notice (4xx) or it ran out of attempts.
    parked_at TIMESTAMPTZ,
    last_error TEXT CHECK (last_error IS NULL OR length(last_error) <= 300),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (vendor_id, version, type)
);
CREATE INDEX vendor_notification_outbox_due_idx ON vendor_notification_outbox (next_attempt_at)
    WHERE delivered_at IS NULL AND parked_at IS NULL;
