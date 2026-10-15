-- AF-16: vendor CSV imports/exports owned by Catalog (products, prices).
-- The job tables are a copy of backend/pkg/bulkimport/schema.sql.
CREATE TABLE import_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id uuid NOT NULL,
    kind text NOT NULL,
    template_version integer NOT NULL CHECK (template_version > 0),
    requested_by uuid NOT NULL,
    idempotency_key text NOT NULL,
    file_name text NOT NULL,
    file_hash text NOT NULL,
    file_bytes bigint NOT NULL CHECK (file_bytes > 0),
    object_key text NOT NULL,
    header jsonb NOT NULL DEFAULT '[]',
    status text NOT NULL DEFAULT 'uploaded' CHECK (status IN ('uploaded','validating','preview_ready','file_invalid','failed','confirmed','applying','cancelling','cancelled','completed')),
    preview_hash text NOT NULL DEFAULT '',
    preview_at timestamptz,
    duplicate_of uuid,
    error_code text,
    error_message text,
    version bigint NOT NULL DEFAULT 1,
    confirmed_by uuid,
    confirmed_at timestamptz,
    cancelled_by uuid,
    finished_at timestamptz,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz,
    file_expires_at timestamptz,
    file_deleted_at timestamptz,
    report_key text,
    report_version bigint,
    report_expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (vendor_id, idempotency_key)
);
CREATE INDEX import_jobs_vendor_idx ON import_jobs (vendor_id, created_at DESC, id DESC);
CREATE INDEX import_jobs_validate_idx ON import_jobs (next_attempt_at, id) WHERE status IN ('uploaded','validating');
CREATE INDEX import_jobs_file_idx ON import_jobs (vendor_id, kind, file_hash);
CREATE INDEX import_jobs_cleanup_idx ON import_jobs (file_expires_at) WHERE file_deleted_at IS NULL;
CREATE INDEX import_jobs_admin_idx ON import_jobs (created_at DESC, id DESC);

CREATE TABLE import_rows (
    job_id uuid NOT NULL REFERENCES import_jobs(id),
    row_no integer NOT NULL CHECK (row_no > 0),
    target_key text NOT NULL DEFAULT '',
    raw jsonb NOT NULL,
    payload jsonb,
    payload_hash text NOT NULL DEFAULT '',
    expected_version bigint,
    old_values jsonb,
    new_values jsonb,
    status text NOT NULL CHECK (status IN ('invalid','unchanged','valid','applying','succeeded','needs_approval','failed','cancelled')),
    operation_ref text NOT NULL,
    result_ref text,
    error_code text,
    error_field text,
    error_message text,
    attempts integer NOT NULL DEFAULT 0,
    lease_until timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (job_id, row_no),
    UNIQUE (operation_ref)
);
CREATE INDEX import_rows_pending_idx ON import_rows (lease_until, job_id, row_no) WHERE status IN ('valid','applying');

-- What the vendor previewed is what is applied: the planned input and the
-- per-row operation reference never change after the dry-run.
CREATE FUNCTION import_rows_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.job_id,NEW.row_no,NEW.raw,NEW.payload,NEW.payload_hash,NEW.expected_version,NEW.target_key,NEW.operation_ref)
       IS DISTINCT FROM (OLD.job_id,OLD.row_no,OLD.raw,OLD.payload,OLD.payload_hash,OLD.expected_version,OLD.target_key,OLD.operation_ref) THEN
        RAISE EXCEPTION 'import row plan is immutable';
    END IF;
    IF OLD.status IN ('succeeded','needs_approval') THEN
        RAISE EXCEPTION 'applied import row is final';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER import_rows_immutable BEFORE UPDATE ON import_rows
    FOR EACH ROW EXECUTE FUNCTION import_rows_immutable();
CREATE FUNCTION import_rows_no_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM import_jobs WHERE id=OLD.job_id AND status NOT IN ('uploaded','validating')) THEN
        RAISE EXCEPTION 'import rows are kept once previewed';
    END IF;
    RETURN OLD;
END $$;
CREATE TRIGGER import_rows_no_delete BEFORE DELETE ON import_rows
    FOR EACH ROW EXECUTE FUNCTION import_rows_no_delete();

CREATE TABLE import_exports (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id uuid NOT NULL,
    kind text NOT NULL,
    requested_by uuid NOT NULL,
    idempotency_key text NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','ready','failed','expired')),
    object_key text NOT NULL,
    row_count integer NOT NULL DEFAULT 0,
    error_code text,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz,
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (vendor_id, idempotency_key)
);
CREATE INDEX import_exports_work_idx ON import_exports (next_attempt_at, id) WHERE status IN ('pending','processing');
CREATE INDEX import_exports_expiry_idx ON import_exports (expires_at) WHERE status='ready';

CREATE TABLE import_job_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id uuid NOT NULL,
    vendor_id uuid NOT NULL,
    actor_id uuid,
    action text NOT NULL,
    reason text,
    detail jsonb NOT NULL DEFAULT '{}',
    request_id text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX import_job_events_job_idx ON import_job_events (job_id, created_at);
CREATE FUNCTION import_job_events_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'import job events are append-only'; END $$;
CREATE TRIGGER import_job_events_append_only BEFORE UPDATE OR DELETE ON import_job_events
    FOR EACH ROW EXECUTE FUNCTION import_job_events_append_only();

-- A vendor's stable client_row_id creates at most one product per shop,
-- across jobs, so re-uploading corrected rows never duplicates a product.
CREATE TABLE product_import_refs (
    vendor_id uuid NOT NULL,
    client_row_id text NOT NULL CHECK (client_row_id ~ '^[A-Za-z0-9._-]{1,64}$'),
    product_id uuid NOT NULL REFERENCES products(id),
    job_id uuid NOT NULL REFERENCES import_jobs(id),
    row_no integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (vendor_id, client_row_id)
);
CREATE TRIGGER product_import_refs_append_only BEFORE UPDATE OR DELETE ON product_import_refs
    FOR EACH ROW EXECUTE FUNCTION import_job_events_append_only();
