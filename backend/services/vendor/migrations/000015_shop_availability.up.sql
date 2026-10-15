-- AF-18: a shop on vacation stops taking new orders while its existing
-- orders, returns, refunds and payouts go on. Separate from moderation:
-- status stays approved/suspended; accepting_orders says whether checkout
-- may start. Every flip bumps vendors.version so it travels on the existing
-- selling-status outbox (vendor.status_changed) and fences checkout.
ALTER TABLE vendors
    ADD COLUMN accepting_orders BOOLEAN NOT NULL DEFAULT TRUE,
    -- Version of the vacation schedule (expected_version of PUT/DELETE).
    ADD COLUMN availability_version BIGINT NOT NULL DEFAULT 0 CHECK (availability_version >= 0);

ALTER TABLE vendor_outbox ADD COLUMN accepting_orders BOOLEAN NOT NULL DEFAULT TRUE;

-- One scheduled or active interval per shop (v1). [starts_at, ends_at),
-- UTC; ends_at NULL means until the owner resumes.
CREATE TABLE vendor_availability_intervals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id UUID NOT NULL REFERENCES vendors (id),
    status TEXT NOT NULL CHECK (status IN ('scheduled', 'active', 'ended', 'cancelled')),
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ CHECK (ends_at IS NULL OR ends_at > starts_at),
    message TEXT NOT NULL DEFAULT '' CHECK (length(message) <= 500),
    created_by UUID NOT NULL,
    updated_by UUID,
    -- Shop selling version when the pause took effect / was lifted.
    paused_version BIGINT,
    resumed_version BIGINT,
    paused_at TIMESTAMPTZ,
    closed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((status = 'active') = (paused_at IS NOT NULL AND closed_at IS NULL) OR status IN ('ended', 'cancelled')),
    CHECK ((status IN ('ended', 'cancelled')) = (closed_at IS NOT NULL))
);
CREATE UNIQUE INDEX vendor_availability_open_idx ON vendor_availability_intervals (vendor_id)
    WHERE status IN ('scheduled', 'active');
CREATE INDEX vendor_availability_due_start_idx ON vendor_availability_intervals (starts_at) WHERE status = 'scheduled';
CREATE INDEX vendor_availability_due_end_idx ON vendor_availability_intervals (ends_at)
    WHERE status = 'active' AND ends_at IS NOT NULL;
CREATE INDEX vendor_availability_history_idx ON vendor_availability_intervals (vendor_id, created_at DESC);

-- Append-only history: who scheduled, changed, cancelled, and when the
-- scheduler paused or resumed (actor NULL = scheduler).
CREATE TABLE vendor_availability_audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id UUID NOT NULL REFERENCES vendors (id),
    interval_id UUID NOT NULL REFERENCES vendor_availability_intervals (id),
    actor_user_id UUID,
    action TEXT NOT NULL CHECK (action IN ('vacation_scheduled', 'vacation_changed', 'vacation_cancelled',
        'vacation_started', 'vacation_ended')),
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ,
    availability_version BIGINT NOT NULL,
    sale_version BIGINT NOT NULL,
    membership_version BIGINT,
    request_id TEXT CHECK (request_id IS NULL OR length(request_id) <= 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX vendor_availability_audit_vendor_idx ON vendor_availability_audit_logs (vendor_id, created_at DESC);
CREATE INDEX vendor_availability_audit_recent_idx ON vendor_availability_audit_logs (created_at DESC, id);
CREATE TRIGGER vendor_availability_audit_append_only BEFORE UPDATE OR DELETE ON vendor_availability_audit_logs
FOR EACH ROW EXECUTE FUNCTION vendor_audit_append_only();

-- The owner is told when the scheduler or a resume flips the shop (one
-- notice per selling version).
ALTER TABLE vendor_notification_outbox DROP CONSTRAINT IF EXISTS vendor_notification_outbox_type_check;
ALTER TABLE vendor_notification_outbox ADD CONSTRAINT vendor_notification_outbox_type_check CHECK (type IN (
    'vendor_approved', 'vendor_rejected', 'marketplace_policy_updated', 'shop_policy_approved', 'shop_policy_rejected',
    'return_destination_verified', 'return_destination_rejected', 'shop_vacation_started', 'shop_vacation_ended'));
