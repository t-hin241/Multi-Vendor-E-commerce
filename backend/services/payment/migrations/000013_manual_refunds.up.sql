-- AF-06: manual bank-transfer refunds. The buyer gives a destination
-- (encrypted, AAD bound to refund + version), finance verifies it, an
-- operator claims an attempt, transfers outside the app, records the bank
-- reference, and a reviewer confirms. Only a confirmed attempt marks the
-- refund succeeded (in the same transaction as the ledger and outbox).

CREATE TABLE refund_destinations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    refund_id UUID NOT NULL REFERENCES payment_refunds (id),
    version INT NOT NULL CHECK (version > 0),
    bank_code TEXT NOT NULL CHECK (bank_code ~ '^[A-Z0-9]{2,20}$'),
    account_last4 TEXT NOT NULL CHECK (account_last4 ~ '^[0-9]{4}$'),
    ciphertext BYTEA NOT NULL,
    key_version INT NOT NULL CHECK (key_version > 0),
    status TEXT NOT NULL CHECK (status IN ('pending_verification', 'verified', 'rejected', 'superseded')),
    submitted_by UUID NOT NULL,
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_by UUID,
    decided_at TIMESTAMPTZ,
    decision_reason TEXT CHECK (decision_reason IS NULL OR length(decision_reason) BETWEEN 1 AND 500),
    UNIQUE (refund_id, version),
    CHECK (status NOT IN ('verified', 'rejected') OR (decided_by IS NOT NULL AND decided_at IS NOT NULL))
);
-- One destination in use per refund at a time.
CREATE UNIQUE INDEX refund_destinations_current_idx ON refund_destinations (refund_id)
    WHERE status IN ('pending_verification', 'verified');
CREATE INDEX refund_destinations_pending_idx ON refund_destinations (submitted_at)
    WHERE status = 'pending_verification';

CREATE TABLE manual_refund_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    refund_id UUID NOT NULL REFERENCES payment_refunds (id),
    destination_id UUID NOT NULL REFERENCES refund_destinations (id),
    destination_version INT NOT NULL CHECK (destination_version > 0),
    amount BIGINT NOT NULL CHECK (amount > 0),
    currency TEXT NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    stage TEXT NOT NULL CHECK (stage IN ('ready', 'executing', 'submitted', 'confirmed', 'failed', 'unknown', 'voided')),
    version INT NOT NULL DEFAULT 1 CHECK (version > 0),
    prepared_by UUID NOT NULL,
    prepare_reason TEXT NOT NULL CHECK (length(prepare_reason) BETWEEN 1 AND 500),
    claimed_by UUID,
    claimed_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,
    source_account TEXT CHECK (source_account IS NULL OR length(source_account) BETWEEN 2 AND 64),
    bank_reference TEXT CHECK (bank_reference IS NULL OR length(bank_reference) BETWEEN 3 AND 100),
    bank_reference_key TEXT,
    executed_at TIMESTAMPTZ,
    submitted_by UUID,
    submitted_at TIMESTAMPTZ,
    decided_by UUID,
    decided_at TIMESTAMPTZ,
    decision_reason TEXT CHECK (decision_reason IS NULL OR length(decision_reason) BETWEEN 1 AND 500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (stage <> 'executing' OR (claimed_by IS NOT NULL AND lease_expires_at IS NOT NULL)),
    CHECK (stage NOT IN ('submitted', 'confirmed') OR (bank_reference_key IS NOT NULL AND source_account IS NOT NULL AND executed_at IS NOT NULL)),
    CHECK (stage NOT IN ('confirmed', 'failed') OR (decided_by IS NOT NULL AND decided_at IS NOT NULL))
);
-- Unknown counts as active: no second transfer while one may have left.
CREATE UNIQUE INDEX manual_refund_attempts_active_idx ON manual_refund_attempts (refund_id)
    WHERE stage IN ('ready', 'executing', 'submitted', 'unknown');
-- A bank reference proves one transfer from one source account.
CREATE UNIQUE INDEX manual_refund_attempts_reference_idx ON manual_refund_attempts (source_account, bank_reference_key)
    WHERE bank_reference_key IS NOT NULL;
CREATE INDEX manual_refund_attempts_refund_idx ON manual_refund_attempts (refund_id, created_at);
CREATE INDEX manual_refund_attempts_lease_idx ON manual_refund_attempts (lease_expires_at) WHERE stage = 'executing';

CREATE TABLE refund_evidence (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    attempt_id UUID NOT NULL REFERENCES manual_refund_attempts (id),
    object_key TEXT NOT NULL UNIQUE,
    content_type TEXT NOT NULL CHECK (content_type IN ('image/jpeg', 'image/png', 'application/pdf')),
    size_bytes INT NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 5242880),
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    uploaded_by UUID NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('uploading', 'uploaded', 'attached', 'deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX refund_evidence_attempt_idx ON refund_evidence (attempt_id, created_at);
CREATE INDEX refund_evidence_orphan_idx ON refund_evidence (created_at) WHERE state IN ('uploading', 'uploaded');

-- History is never rewritten: no deletes; the encrypted destination, the
-- attempt's target and amount, and finished attempts stay as written.
CREATE FUNCTION manual_refund_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION '% rows are never deleted', TG_TABLE_NAME;
    END IF;
    IF TG_TABLE_NAME = 'refund_destinations' THEN
        IF NEW.refund_id <> OLD.refund_id OR NEW.version <> OLD.version OR NEW.ciphertext <> OLD.ciphertext
           OR NEW.key_version <> OLD.key_version OR NEW.bank_code <> OLD.bank_code OR NEW.account_last4 <> OLD.account_last4
           OR NEW.submitted_by <> OLD.submitted_by THEN
            RAISE EXCEPTION 'a refund destination is immutable; submit a new version';
        END IF;
        IF OLD.status IN ('rejected', 'superseded') THEN
            RAISE EXCEPTION 'a % refund destination is final', OLD.status;
        END IF;
    ELSIF TG_TABLE_NAME = 'manual_refund_attempts' THEN
        IF NEW.refund_id <> OLD.refund_id OR NEW.destination_id <> OLD.destination_id OR NEW.amount <> OLD.amount
           OR NEW.currency <> OLD.currency OR NEW.prepared_by <> OLD.prepared_by THEN
            RAISE EXCEPTION 'a manual refund attempt keeps its target and amount';
        END IF;
        IF OLD.stage IN ('confirmed', 'failed', 'voided') THEN
            RAISE EXCEPTION 'a % manual refund attempt is final', OLD.stage;
        END IF;
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER refund_destinations_guard BEFORE UPDATE OR DELETE ON refund_destinations
FOR EACH ROW EXECUTE FUNCTION manual_refund_guard();
CREATE TRIGGER manual_refund_attempts_guard BEFORE UPDATE OR DELETE ON manual_refund_attempts
FOR EACH ROW EXECUTE FUNCTION manual_refund_guard();

CREATE FUNCTION refund_evidence_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'refund evidence rows are never deleted';
    END IF;
    IF NEW.attempt_id <> OLD.attempt_id OR NEW.object_key <> OLD.object_key OR NEW.sha256 <> OLD.sha256 THEN
        RAISE EXCEPTION 'refund evidence is immutable';
    END IF;
    IF OLD.state = 'attached' AND NEW.state <> 'attached' THEN
        RAISE EXCEPTION 'attached refund evidence is kept';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER refund_evidence_guard BEFORE UPDATE OR DELETE ON refund_evidence
FOR EACH ROW EXECUTE FUNCTION refund_evidence_guard();
