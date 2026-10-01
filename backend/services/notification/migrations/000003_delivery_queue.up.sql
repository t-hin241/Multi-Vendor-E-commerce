-- NTF-01/03/04: a notification is recorded (pending) before anything is
-- sent and is the durable job itself: a worker claims due rows with a
-- lease, records every attempt, retries transient failures with backoff,
-- stops on permanent ones and parks a notification after its last attempt.
-- One row per (source, event, recipient, type, template version).
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_status_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_status_check
    CHECK (status IN ('pending', 'sending', 'sent', 'failed', 'parked'));
ALTER TABLE notifications
    ADD COLUMN event_id TEXT,
    ADD COLUMN source TEXT NOT NULL DEFAULT 'legacy',
    ADD COLUMN dedup_key TEXT,
    ADD COLUMN template_version TEXT NOT NULL DEFAULT 'v1',
    ADD COLUMN correlation_id TEXT CHECK (correlation_id IS NULL OR length(correlation_id) <= 64),
    -- e.g. "b***@example.com": enough to tell recipients apart, not the address.
    ADD COLUMN recipient_masked TEXT,
    ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    ADD COLUMN max_attempts INTEGER NOT NULL DEFAULT 8 CHECK (max_attempts BETWEEN 1 AND 50),
    ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN lease_until TIMESTAMPTZ,
    ADD COLUMN sent_at TIMESTAMPTZ,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE notifications ADD CONSTRAINT notifications_fail_reason_length CHECK (fail_reason IS NULL OR length(fail_reason) <= 300) NOT VALID;

-- Rows written by the previous version were sent (or failed) synchronously
-- and are final; each keeps its own key, so nothing is resent.
UPDATE notifications SET event_id = 'legacy:' || id::text, dedup_key = 'legacy:' || id::text,
    sent_at = CASE WHEN status = 'sent' THEN created_at END, updated_at = created_at
WHERE dedup_key IS NULL;
ALTER TABLE notifications ALTER COLUMN dedup_key SET NOT NULL;
ALTER TABLE notifications ALTER COLUMN event_id SET NOT NULL;
-- An image rolled back to the previous version still records what it
-- sends (it does not know these columns).
ALTER TABLE notifications ALTER COLUMN dedup_key SET DEFAULT ('legacy:' || gen_random_uuid()::text);
ALTER TABLE notifications ALTER COLUMN event_id SET DEFAULT ('legacy:' || gen_random_uuid()::text);
CREATE UNIQUE INDEX notifications_dedup_key ON notifications (dedup_key);
CREATE INDEX notifications_due_idx ON notifications (next_attempt_at) WHERE status IN ('pending', 'sending');
CREATE INDEX notifications_status_idx ON notifications (status, updated_at DESC);

-- One row per delivery attempt; error is a short sanitized reason (never
-- the address, body, token or provider credentials).
CREATE TABLE notification_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    notification_id UUID NOT NULL REFERENCES notifications (id),
    attempt INTEGER NOT NULL CHECK (attempt >= 1),
    outcome TEXT NOT NULL CHECK (outcome IN ('sent', 'retry', 'failed', 'parked')),
    error TEXT CHECK (error IS NULL OR length(error) <= 300),
    duration_ms INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX notification_attempts_notification_idx ON notification_attempts (notification_id, created_at);
CREATE INDEX notification_attempts_created_idx ON notification_attempts (created_at);

-- ADM-01: an admin's manual retry is audited in the same transaction.
CREATE TABLE notification_admin_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id UUID NOT NULL,
    action TEXT NOT NULL CHECK (action ~ '^[a-z_]{1,60}$'),
    entity_type TEXT NOT NULL CHECK (entity_type IN ('notification')),
    entity_id TEXT NOT NULL,
    reason TEXT CHECK (reason IS NULL OR length(reason) <= 1000),
    changes JSONB,
    request_id TEXT CHECK (request_id IS NULL OR length(request_id) <= 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX notification_audit_recent_idx ON notification_admin_audit (created_at DESC, id);
CREATE INDEX notification_audit_actor_idx ON notification_admin_audit (actor_id, created_at DESC);
CREATE INDEX notification_audit_entity_idx ON notification_admin_audit (entity_id);
CREATE INDEX notification_audit_request_idx ON notification_admin_audit (request_id) WHERE request_id IS NOT NULL;
CREATE FUNCTION notification_audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Audit rows are append-only';
END $$;
CREATE TRIGGER notification_audit_append_only BEFORE UPDATE OR DELETE ON notification_admin_audit
FOR EACH ROW EXECUTE FUNCTION notification_audit_append_only();
