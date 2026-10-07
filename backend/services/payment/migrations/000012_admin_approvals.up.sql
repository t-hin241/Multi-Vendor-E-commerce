-- AF-19: maker-checker for manual money actions. While Payment's
-- FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED is on, recording a refund or
-- payout result and posting a ledger adjustment need an immutable request
-- by one admin and an approval by another; approval and execution commit
-- together.
CREATE TABLE approval_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_kind TEXT NOT NULL CHECK (operation_kind IN ('refund_resolution', 'payout_item_resolution', 'settlement_adjustment')),
    target_id TEXT NOT NULL CHECK (length(target_id) BETWEEN 1 AND 100),
    payload JSONB NOT NULL,
    payload_hash TEXT NOT NULL CHECK (payload_hash ~ '^[0-9a-f]{64}$'),
    snapshot JSONB NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('draft', 'pending', 'approved', 'rejected', 'expired', 'cancelled')),
    maker_id UUID NOT NULL,
    maker_permission_version BIGINT NOT NULL,
    reason TEXT NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
    checker_id UUID,
    checker_permission_version BIGINT,
    decision_reason TEXT CHECK (decision_reason IS NULL OR length(decision_reason) <= 500),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    submitted_at TIMESTAMPTZ,
    decided_at TIMESTAMPTZ,
    -- The executed operation's reference; unique, so one approval executes once.
    execution_ref TEXT UNIQUE,
    CHECK (checker_id IS NULL OR checker_id <> maker_id),
    CHECK ((status = 'approved') = (execution_ref IS NOT NULL))
);
CREATE UNIQUE INDEX approval_requests_open_idx ON approval_requests (operation_kind, target_id) WHERE status IN ('draft', 'pending');
CREATE INDEX approval_requests_queue_idx ON approval_requests (status, created_at DESC, id);

-- What was asked never changes after creation.
CREATE FUNCTION approval_request_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.operation_kind IS DISTINCT FROM OLD.operation_kind OR NEW.target_id IS DISTINCT FROM OLD.target_id
        OR NEW.payload IS DISTINCT FROM OLD.payload OR NEW.payload_hash IS DISTINCT FROM OLD.payload_hash
        OR NEW.snapshot IS DISTINCT FROM OLD.snapshot OR NEW.maker_id IS DISTINCT FROM OLD.maker_id
        OR NEW.reason IS DISTINCT FROM OLD.reason OR NEW.expires_at IS DISTINCT FROM OLD.expires_at THEN
        RAISE EXCEPTION 'approval request content is immutable';
    END IF;
    IF OLD.status IN ('approved', 'rejected', 'expired', 'cancelled') THEN
        RAISE EXCEPTION 'approval request is final';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER approval_request_immutable BEFORE UPDATE ON approval_requests
FOR EACH ROW EXECUTE FUNCTION approval_request_immutable();
CREATE TRIGGER approval_request_no_delete BEFORE DELETE ON approval_requests
FOR EACH ROW EXECUTE FUNCTION payment_audit_append_only();
