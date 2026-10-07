-- AF-01: support cases tied to an order. A buyer opens a case on one vendor
-- order; the selling shop answers its public part; admins coordinate,
-- keep internal notes and resolve it, linking the refund or return that
-- carries any money or goods (a case never moves money itself). Messages
-- and history are append-only. A case that may affect money holds the
-- vendor order's payout while it is not closed (see HeldForSettlement).

CREATE TABLE support_cases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES orders (id),
    vendor_order_id UUID NOT NULL REFERENCES vendor_orders (id),
    -- Copied from the vendor order at creation; never changes.
    vendor_id UUID NOT NULL,
    buyer_id UUID NOT NULL,
    category TEXT NOT NULL CHECK (category IN ('not_received', 'missing_items', 'wrong_items', 'damaged', 'payment_issue', 'other')),
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN (
        'open', 'in_progress', 'waiting_buyer', 'waiting_vendor', 'resolution_pending', 'resolved', 'closed')),
    assignee_id UUID,
    policy_version TEXT NOT NULL CHECK (length(policy_version) BETWEEN 1 AND 40),
    due_at TIMESTAMPTZ,
    -- The case may change what is owed to the vendor: its payout is held.
    financial_hold BOOLEAN NOT NULL,
    resolution_kind TEXT CHECK (resolution_kind IN ('no_action', 'refund', 'return')),
    -- The refund or return id carrying the resolution (no_action: none).
    resolution_ref UUID,
    resolution_note TEXT CHECK (resolution_note IS NULL OR length(resolution_note) <= 1000),
    resolved_at TIMESTAMPTZ,
    closed_at TIMESTAMPTZ,
    -- A case opened after an earlier one on the same order was closed.
    related_case_id UUID REFERENCES support_cases (id),
    idempotency_key TEXT CHECK (idempotency_key IS NULL OR idempotency_key ~ '^[A-Za-z0-9._:-]{8,100}$'),
    request_hash TEXT,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((resolution_kind IN ('refund', 'return')) = (resolution_ref IS NOT NULL) OR resolution_kind IS NULL)
);
-- One case not yet closed per buyer, vendor order and category.
CREATE UNIQUE INDEX support_cases_open_key ON support_cases (buyer_id, vendor_order_id, category) WHERE status <> 'closed';
CREATE UNIQUE INDEX support_cases_idempotency_key ON support_cases (buyer_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX support_cases_queue_idx ON support_cases (assignee_id, status, due_at, id);
CREATE INDEX support_cases_buyer_idx ON support_cases (buyer_id, created_at DESC, id DESC);
CREATE INDEX support_cases_vendor_idx ON support_cases (vendor_id, created_at DESC, id DESC);
CREATE INDEX support_cases_order_idx ON support_cases (order_id);
CREATE INDEX support_cases_hold_idx ON support_cases (vendor_order_id) WHERE financial_hold AND status <> 'closed';
CREATE INDEX support_cases_resolution_idx ON support_cases (resolution_ref) WHERE status = 'resolution_pending';
CREATE INDEX support_cases_recent_idx ON support_cases (created_at DESC, id DESC);

CREATE TABLE support_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id UUID NOT NULL REFERENCES support_cases (id),
    author_id UUID NOT NULL,
    author_role TEXT NOT NULL CHECK (author_role IN ('buyer', 'vendor', 'admin')),
    -- internal: admin notes, never shown to the buyer or the vendor.
    visibility TEXT NOT NULL CHECK (visibility IN ('public', 'internal')),
    text TEXT NOT NULL CHECK (length(text) BETWEEN 1 AND 4000),
    idempotency_key TEXT CHECK (idempotency_key IS NULL OR idempotency_key ~ '^[A-Za-z0-9._:-]{8,100}$'),
    request_hash TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (visibility = 'public' OR author_role = 'admin')
);
CREATE INDEX support_messages_case_idx ON support_messages (case_id, created_at, id);
CREATE UNIQUE INDEX support_messages_idempotency_key ON support_messages (author_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

-- Evidence images in the private bucket. Uploaded first (state uploaded),
-- attached to one message, deleted (object removed) by the orphan or
-- retention sweep; the row stays as a tombstone.
CREATE TABLE case_attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id UUID NOT NULL,
    case_id UUID REFERENCES support_cases (id),
    message_id UUID REFERENCES support_messages (id),
    object_key TEXT NOT NULL UNIQUE CHECK (length(object_key) BETWEEN 1 AND 200),
    content_type TEXT NOT NULL CHECK (content_type IN ('image/jpeg', 'image/png')),
    size_bytes BIGINT NOT NULL CHECK (size_bytes BETWEEN 1 AND 5242880),
    state TEXT NOT NULL DEFAULT 'uploaded' CHECK (state IN ('uploaded', 'attached', 'deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    attached_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    CHECK ((state = 'attached') = (message_id IS NOT NULL) OR state = 'deleted')
);
CREATE INDEX case_attachments_message_idx ON case_attachments (message_id) WHERE message_id IS NOT NULL;
CREATE INDEX case_attachments_orphan_idx ON case_attachments (created_at) WHERE state = 'uploaded';

CREATE TABLE support_case_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id UUID NOT NULL REFERENCES support_cases (id),
    actor_id UUID,
    actor_role TEXT NOT NULL CHECK (actor_role IN ('buyer', 'vendor', 'admin', 'system')),
    action TEXT NOT NULL CHECK (action ~ '^[a-z_]{1,60}$'),
    from_status TEXT,
    to_status TEXT NOT NULL,
    -- Shown to admins only.
    note TEXT CHECK (note IS NULL OR length(note) <= 1000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX support_case_history_case_idx ON support_case_history (case_id, created_at, id);

CREATE FUNCTION support_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Support messages and history are append-only';
END $$;
CREATE TRIGGER support_messages_append_only BEFORE UPDATE OR DELETE ON support_messages
FOR EACH ROW EXECUTE FUNCTION support_append_only();
CREATE TRIGGER support_case_history_append_only BEFORE UPDATE OR DELETE ON support_case_history
FOR EACH ROW EXECUTE FUNCTION support_append_only();

ALTER TABLE order_admin_audit DROP CONSTRAINT IF EXISTS order_admin_audit_entity_type_check;
ALTER TABLE order_admin_audit ADD CONSTRAINT order_admin_audit_entity_type_check
    CHECK (entity_type IN ('order', 'refund', 'return_request', 'effect', 'commission_rule', 'support_case'));
