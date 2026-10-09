-- AF-08: notices about work a shop has to do. A producer's event (Order,
-- Payment) is recorded once per (source, event_id); a worker then asks
-- Vendor who may receive it, keeps that list on the row and writes one
-- notification per person in the same transaction, so a crash never
-- resolves the same event to a second, different list. An event no one may
-- receive (owner locked, shop gone) waits for an admin as no_recipient; an
-- event Vendor could not answer for is retried, then parked.
CREATE TABLE vendor_action_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source TEXT NOT NULL CHECK (source IN ('order', 'payment')),
    event_id TEXT NOT NULL CHECK (event_id ~ '^[A-Za-z0-9._:-]{1,100}$'),
    vendor_id UUID NOT NULL,
    action_kind TEXT NOT NULL CHECK (action_kind ~ '^[a-z_]{1,40}$'),
    purpose TEXT NOT NULL CHECK (purpose IN ('orders', 'returns', 'finance')),
    reference_id TEXT NOT NULL CHECK (reference_id ~ '^[A-Za-z0-9._:-]{1,100}$'),
    vendor_order_id UUID,
    correlation_id TEXT CHECK (correlation_id IS NULL OR length(correlation_id) <= 64),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'resolving', 'resolved', 'no_recipient', 'parked')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_until TIMESTAMPTZ,
    last_error TEXT CHECK (last_error IS NULL OR length(last_error) <= 300),
    -- The people the notices were written for, and Vendor's version of
    -- that list; set once, when resolved.
    recipient_user_ids UUID[] NOT NULL DEFAULT '{}',
    permission_version TEXT CHECK (permission_version IS NULL OR length(permission_version) <= 64),
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source, event_id)
);
CREATE INDEX vendor_action_events_due_idx ON vendor_action_events (next_attempt_at) WHERE status IN ('pending', 'resolving');
CREATE INDEX vendor_action_events_review_idx ON vendor_action_events (status, updated_at DESC);
CREATE INDEX vendor_action_events_vendor_idx ON vendor_action_events (vendor_id, created_at DESC);

-- Optional shop notice categories a person chose (staff opt in; the owner
-- receives every category regardless). Work notices are transactional:
-- turning off marketing never touches them.
CREATE TABLE notification_preferences (
    user_id UUID PRIMARY KEY,
    vendor_categories TEXT[] NOT NULL DEFAULT '{}'
        CHECK (vendor_categories <@ ARRAY['orders', 'returns', 'finance']::text[]),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- An admin's retry of a shop notice is audited like a notification retry.
ALTER TABLE notification_admin_audit DROP CONSTRAINT IF EXISTS notification_admin_audit_entity_type_check;
ALTER TABLE notification_admin_audit ADD CONSTRAINT notification_admin_audit_entity_type_check
    CHECK (entity_type IN ('notification', 'vendor_action_event'));
