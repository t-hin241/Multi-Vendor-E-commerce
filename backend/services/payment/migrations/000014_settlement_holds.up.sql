-- PW-001 (00 §6.1): Payment owns the settlement hold ledger. A source in
-- Order (a support case, later returns/cancellations) acquires a hold on a
-- vendor order before it tells anyone the money is protected; payout batch
-- creation checks active holds under the same vendor lock, so a hold and a
-- payout claim never interleave. A release before its acquire (late retry)
-- leaves a tombstone that a later acquire cannot reopen.
CREATE TABLE settlement_holds (
    id UUID PRIMARY KEY,
    vendor_id UUID,
    vendor_order_id UUID,
    source_type TEXT CHECK (source_type IS NULL OR source_type ~ '^[a-z_]{2,40}$'),
    source_id UUID,
    source_version BIGINT,
    reason_code TEXT CHECK (reason_code IS NULL OR reason_code ~ '^[a-z_]{2,60}$'),
    status TEXT NOT NULL CHECK (status IN ('active', 'released')),
    -- A payout had already claimed this vendor order's credits when the
    -- hold was acquired: the source needs a manual review.
    payout_claimed BOOLEAN NOT NULL DEFAULT false,
    acquired_at TIMESTAMPTZ,
    release_operation_id TEXT CHECK (release_operation_id IS NULL OR length(release_operation_id) BETWEEN 1 AND 100),
    release_source_version BIGINT,
    resolution_ref TEXT CHECK (resolution_ref IS NULL OR length(resolution_ref) <= 100),
    release_reason TEXT CHECK (release_reason IS NULL OR length(release_reason) <= 500),
    released_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status <> 'active' OR (vendor_id IS NOT NULL AND vendor_order_id IS NOT NULL AND source_type IS NOT NULL
        AND source_id IS NOT NULL AND acquired_at IS NOT NULL)),
    CHECK (status <> 'released' OR released_at IS NOT NULL)
);
CREATE UNIQUE INDEX settlement_holds_source_idx ON settlement_holds (source_type, source_id, vendor_order_id)
    WHERE source_id IS NOT NULL;
CREATE INDEX settlement_holds_active_idx ON settlement_holds (vendor_order_id) WHERE status = 'active';

-- A released hold stays released, and no hold is ever deleted.
CREATE FUNCTION settlement_hold_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'settlement holds are never deleted';
    END IF;
    IF OLD.status = 'released' THEN
        RAISE EXCEPTION 'a released settlement hold is final';
    END IF;
    IF NEW.vendor_order_id IS DISTINCT FROM OLD.vendor_order_id OR NEW.source_id IS DISTINCT FROM OLD.source_id
       OR NEW.vendor_id IS DISTINCT FROM OLD.vendor_id THEN
        RAISE EXCEPTION 'a settlement hold keeps its source and vendor order';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER settlement_hold_guard BEFORE UPDATE OR DELETE ON settlement_holds
FOR EACH ROW EXECUTE FUNCTION settlement_hold_guard();
