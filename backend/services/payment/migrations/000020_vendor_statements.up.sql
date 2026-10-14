-- AF-14: historical rows retain an unknown occurrence time; no guessed money/currency.
ALTER TABLE settlement_entries ADD COLUMN occurred_at TIMESTAMPTZ;
CREATE INDEX settlement_entries_statement_idx ON settlement_entries (vendor_id, currency, created_at, id);
CREATE INDEX settlement_entries_source_idx ON settlement_entries (source_ref);

CREATE TABLE statement_exports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id UUID NOT NULL,
    requester_id UUID NOT NULL,
    idempotency_key TEXT NOT NULL,
    filters JSONB NOT NULL,
    cutoff TIMESTAMPTZ NOT NULL,
    snapshot JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','ready','failed','expired')),
    object_key TEXT NOT NULL,
    checksum TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_until TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (vendor_id, idempotency_key)
);
CREATE INDEX statement_exports_work_idx ON statement_exports (next_attempt_at) WHERE status IN ('pending','processing');
CREATE TABLE statement_export_entries (
    export_id UUID NOT NULL REFERENCES statement_exports(id),
    position INTEGER NOT NULL,
    entry JSONB NOT NULL,
    PRIMARY KEY (export_id, position)
);
CREATE TRIGGER statement_export_entries_immutable BEFORE UPDATE ON statement_export_entries
    FOR EACH ROW EXECUTE FUNCTION settlement_append_only();

CREATE FUNCTION statement_export_snapshot_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.checksum <> '' AND NEW.checksum <> OLD.checksum THEN
        RAISE EXCEPTION 'statement checksum is immutable once sealed';
    END IF;
    IF (NEW.vendor_id,NEW.requester_id,NEW.idempotency_key,NEW.filters,NEW.cutoff,NEW.snapshot,NEW.object_key,NEW.expires_at)
       IS DISTINCT FROM (OLD.vendor_id,OLD.requester_id,OLD.idempotency_key,OLD.filters,OLD.cutoff,OLD.snapshot,OLD.object_key,OLD.expires_at) THEN
        RAISE EXCEPTION 'statement snapshot is immutable';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER statement_export_snapshot_immutable BEFORE UPDATE ON statement_exports
    FOR EACH ROW EXECUTE FUNCTION statement_export_snapshot_immutable();
