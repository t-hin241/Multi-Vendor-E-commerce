-- AF-02: versioned marketplace and shop policies. Vendor owns the text;
-- the services that enforce a rule (Order today) acknowledge the rule
-- versions a policy refers to before it is published, so the published
-- text never promises what the code does not do. A published version is
-- immutable; a correction is a new version. Orders snapshot the version in
-- force when they are placed (Order keeps a read model fed by
-- vendor.policy_published).

CREATE TABLE marketplace_policy_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Global sequence, used to deduplicate per-version shop notices.
    seq BIGSERIAL NOT NULL UNIQUE,
    kind TEXT NOT NULL CHECK (kind IN ('returns', 'shipping', 'terms', 'privacy')),
    version INTEGER NOT NULL CHECK (version > 0),
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    summary TEXT NOT NULL CHECK (length(summary) BETWEEN 1 AND 1000),
    content TEXT NOT NULL CHECK (length(content) BETWEEN 1 AND 50000),
    contact TEXT NOT NULL CHECK (length(contact) BETWEEN 1 AND 1000),
    -- Rule versions this text describes, e.g. {"order.returns_window": "window-7d"}.
    rule_refs JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(rule_refs) = 'object'),
    effective_at TIMESTAMPTZ NOT NULL,
    -- draft: editable by nobody (a new draft replaces it); preparing: waiting
    -- for every rule owner to acknowledge; published: public from
    -- effective_at; withdrawn: a draft or preparing version given up.
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'preparing', 'published', 'withdrawn')),
    content_hash TEXT NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    -- Last answer of each rule owner: {"order.returns_window": {"ready": true, "rule_hash": "..."}}.
    readiness JSONB,
    publication_reason TEXT CHECK (publication_reason IS NULL OR length(publication_reason) <= 500),
    created_by UUID NOT NULL,
    published_by UUID,
    preparing_since TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    row_version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kind, version)
);
-- One published version per kind and start time: the active version is the
-- published one with the latest effective_at not in the future.
CREATE UNIQUE INDEX marketplace_policy_versions_effective_key ON marketplace_policy_versions (kind, effective_at)
    WHERE status = 'published';
CREATE INDEX marketplace_policy_versions_preparing_idx ON marketplace_policy_versions (preparing_since) WHERE status = 'preparing';

CREATE FUNCTION policy_version_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'DELETE' THEN
  IF OLD.status = 'published' THEN
   RAISE EXCEPTION 'A published policy version cannot be deleted';
  END IF;
  RETURN OLD;
 END IF;
 IF OLD.status = 'published' THEN
  RAISE EXCEPTION 'A published policy version is immutable; publish a new version';
 END IF;
 IF (NEW.kind, NEW.version, NEW.title, NEW.summary, NEW.content, NEW.contact, NEW.rule_refs, NEW.effective_at, NEW.content_hash)
    IS DISTINCT FROM (OLD.kind, OLD.version, OLD.title, OLD.summary, OLD.content, OLD.contact, OLD.rule_refs, OLD.effective_at, OLD.content_hash) THEN
  RAISE EXCEPTION 'Policy content is fixed when the draft is created; create a new draft';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER marketplace_policy_versions_immutable BEFORE UPDATE OR DELETE ON marketplace_policy_versions
FOR EACH ROW EXECUTE FUNCTION policy_version_immutable();

-- A shop's own addition to the marketplace policies. It cannot change a
-- money or deadline rule (no rule_refs in this version) and is public only
-- once an admin approved it.
CREATE TABLE shop_policy_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seq BIGSERIAL NOT NULL UNIQUE,
    vendor_id UUID NOT NULL REFERENCES vendors (id),
    version INTEGER NOT NULL CHECK (version > 0),
    content TEXT NOT NULL CHECK (length(content) BETWEEN 1 AND 10000),
    content_hash TEXT NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    status TEXT NOT NULL DEFAULT 'proposed' CHECK (status IN ('proposed', 'approved', 'rejected')),
    -- legacy: copied from the free-text policy_text by this migration.
    source TEXT NOT NULL DEFAULT 'vendor' CHECK (source IN ('vendor', 'legacy')),
    proposed_by UUID,
    decided_by UUID,
    decision_reason TEXT CHECK (decision_reason IS NULL OR length(decision_reason) <= 500),
    decided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (vendor_id, version)
);
CREATE UNIQUE INDEX shop_policy_versions_open_key ON shop_policy_versions (vendor_id) WHERE status = 'proposed';
CREATE INDEX shop_policy_versions_queue_idx ON shop_policy_versions (created_at, id) WHERE status = 'proposed';
CREATE INDEX shop_policy_versions_approved_idx ON shop_policy_versions (vendor_id, decided_at DESC) WHERE status = 'approved';

CREATE FUNCTION shop_policy_version_decided_once() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'DELETE' OR OLD.status <> 'proposed' OR NEW.content IS DISTINCT FROM OLD.content
    OR NEW.vendor_id IS DISTINCT FROM OLD.vendor_id OR NEW.version IS DISTINCT FROM OLD.version THEN
  RAISE EXCEPTION 'A shop policy version is decided once and never edited';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER shop_policy_versions_decided_once BEFORE UPDATE OR DELETE ON shop_policy_versions
FOR EACH ROW EXECUTE FUNCTION shop_policy_version_decided_once();

-- Existing free text was public without review: it becomes a proposal an
-- admin must decide; nothing is approved on its behalf.
INSERT INTO shop_policy_versions (vendor_id, version, content, content_hash, source)
SELECT id, 1, left(btrim(policy_text), 10000), encode(sha256(convert_to(left(btrim(policy_text), 10000), 'UTF8')), 'hex'), 'legacy'
FROM vendors WHERE btrim(coalesce(policy_text, '')) <> '';

-- vendor.policy_published, committed with the publication.
CREATE TABLE policy_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_id UUID NOT NULL UNIQUE,
    payload JSONB NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_until TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    last_error TEXT CHECK (last_error IS NULL OR length(last_error) <= 300),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX policy_outbox_due_idx ON policy_outbox (next_attempt_at) WHERE delivered_at IS NULL;

-- Admin decisions on marketplace policies (shop decisions stay in
-- vendor_audit_logs with the shop).
CREATE TABLE policy_audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_id UUID NOT NULL REFERENCES marketplace_policy_versions (id),
    actor_user_id UUID NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('policy_drafted', 'policy_publication_requested', 'policy_published', 'policy_withdrawn')),
    reason TEXT CHECK (reason IS NULL OR length(reason) <= 500),
    request_id TEXT CHECK (request_id IS NULL OR length(request_id) <= 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX policy_audit_logs_recent_idx ON policy_audit_logs (created_at DESC, id);
CREATE TRIGGER policy_audit_append_only BEFORE UPDATE OR DELETE ON policy_audit_logs
FOR EACH ROW EXECUTE FUNCTION vendor_audit_append_only();

ALTER TABLE vendor_audit_logs DROP CONSTRAINT vendor_audit_logs_action_check;
ALTER TABLE vendor_audit_logs ADD CONSTRAINT vendor_audit_logs_action_check CHECK(action IN
 ('approved','rejected','suspended','resubmitted','restored','payout_submitted','payout_verified','payout_rejected','payout_disabled',
  'payout_details_read','event_replayed','shop_policy_approved','shop_policy_rejected'));

ALTER TABLE vendor_notification_outbox DROP CONSTRAINT IF EXISTS vendor_notification_outbox_type_check;
ALTER TABLE vendor_notification_outbox ADD CONSTRAINT vendor_notification_outbox_type_check CHECK (type IN (
    'vendor_approved', 'vendor_rejected', 'marketplace_policy_updated', 'shop_policy_approved', 'shop_policy_rejected'));
