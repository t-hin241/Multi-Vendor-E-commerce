-- AF-09: each person's inbox, a read model of the notices Notification
-- records. An item is written in the transaction that records the notice
-- (one per source event, recipient and kind), so a redelivered event never
-- makes it unread again. Read and hidden are the person's own state; the
-- email's delivery status is separate. Items older than the retention are
-- purged in batches (INBOX_RETENTION_DAYS, default 90).
CREATE TABLE inbox_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    recipient_id UUID NOT NULL,
    source TEXT NOT NULL CHECK (source ~ '^[a-z]{1,30}$'),
    event_id TEXT NOT NULL CHECK (event_id ~ '^[A-Za-z0-9._:-]{1,100}$'),
    kind TEXT NOT NULL CHECK (kind ~ '^[a-z_]{1,60}$'),
    template_version TEXT NOT NULL CHECK (length(template_version) <= 20),
    -- Plain text rendered from the template when the notice was recorded.
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    body TEXT NOT NULL CHECK (length(body) <= 1000),
    reference_type TEXT NOT NULL CHECK (reference_type ~ '^[a-z_]{1,40}$'),
    reference_id TEXT NOT NULL CHECK (reference_id ~ '^[A-Za-z0-9._:-]{1,100}$'),
    -- An app route ("/orders/..."), never an absolute URL or a credential.
    link TEXT NOT NULL CHECK (link ~ '^/[A-Za-z0-9/_?=&.-]*$' AND length(link) <= 300),
    read_at TIMESTAMPTZ,
    hidden_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source, event_id, recipient_id, kind)
);
CREATE INDEX inbox_items_recipient_idx ON inbox_items (recipient_id, created_at DESC, id DESC) WHERE hidden_at IS NULL;
CREATE INDEX inbox_items_unread_idx ON inbox_items (recipient_id) WHERE read_at IS NULL AND hidden_at IS NULL;
CREATE INDEX inbox_items_created_idx ON inbox_items (created_at);

-- Marketing consent (AF-09 preferences): separate from transactional
-- notices, which are never turned off by it. Every change is audited.
ALTER TABLE notification_preferences
    ADD COLUMN marketing_opt_in BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN marketing_consented_at TIMESTAMPTZ,
    ADD COLUMN marketing_withdrawn_at TIMESTAMPTZ;
CREATE TABLE notification_consent_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    consent TEXT NOT NULL CHECK (consent IN ('marketing')),
    granted BOOLEAN NOT NULL,
    preference_version BIGINT NOT NULL,
    request_id TEXT CHECK (request_id IS NULL OR length(request_id) <= 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX notification_consent_audit_user_idx ON notification_consent_audit (user_id, created_at DESC);
CREATE FUNCTION notification_consent_audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Audit rows are append-only';
END $$;
CREATE TRIGGER notification_consent_audit_append_only BEFORE UPDATE OR DELETE ON notification_consent_audit
FOR EACH ROW EXECUTE FUNCTION notification_consent_audit_append_only();
