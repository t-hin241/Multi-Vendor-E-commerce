-- PLT-03: inbox of the events this service consumes from the event bus
-- (pkg/eventbus.InboxSQL): one row per consumer and event, written in the
-- transaction that applies it; parked events stay for replay or discard,
-- and every operator action is audited (append-only).
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE TABLE IF NOT EXISTS event_inbox (
    consumer TEXT NOT NULL CHECK (consumer ~ '^[a-z0-9_-]{1,64}$'),
    event_id TEXT NOT NULL CHECK (length(event_id) <= 128),
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('processed', 'parked', 'discarded')),
    attempts INTEGER NOT NULL DEFAULT 1,
    envelope JSONB,
    last_error TEXT CHECK (last_error IS NULL OR length(last_error) <= 500),
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ,
    parked_at TIMESTAMPTZ,
    PRIMARY KEY (consumer, event_id)
);
CREATE INDEX IF NOT EXISTS event_inbox_parked_idx ON event_inbox (parked_at) WHERE status = 'parked';
CREATE INDEX IF NOT EXISTS event_inbox_received_idx ON event_inbox (received_at) WHERE status = 'processed';
CREATE TABLE IF NOT EXISTS event_inbox_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id UUID NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('event_replayed', 'event_discarded')),
    consumer TEXT NOT NULL,
    event_id TEXT NOT NULL,
    reason TEXT NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
    request_id TEXT CHECK (request_id IS NULL OR length(request_id) <= 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS event_inbox_audit_recent_idx ON event_inbox_audit (created_at DESC, id);
CREATE OR REPLACE FUNCTION event_inbox_audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Audit rows are append-only';
END $$;
DROP TRIGGER IF EXISTS event_inbox_audit_append_only ON event_inbox_audit;
CREATE TRIGGER event_inbox_audit_append_only BEFORE UPDATE OR DELETE ON event_inbox_audit
FOR EACH ROW EXECUTE FUNCTION event_inbox_audit_append_only();
