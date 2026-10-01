-- ADM-01/04: audit rows carry the request id that caused them (searchable
-- correlation), are indexed for the paginated admin audit search and can
-- no longer be updated or deleted. Existing rows keep request_id NULL: the
-- id was never recorded, and no value is invented for them.
ALTER TABLE identity_audit_logs ADD COLUMN request_id TEXT CHECK (request_id IS NULL OR length(request_id) <= 64);
CREATE INDEX identity_audit_recent_idx ON identity_audit_logs (created_at DESC, id);
CREATE INDEX identity_audit_actor_idx ON identity_audit_logs (actor_id, created_at DESC);
CREATE INDEX identity_audit_request_idx ON identity_audit_logs (request_id) WHERE request_id IS NOT NULL;
CREATE FUNCTION identity_audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Audit rows are append-only';
END $$;
CREATE TRIGGER identity_audit_append_only BEFORE UPDATE OR DELETE ON identity_audit_logs
FOR EACH ROW EXECUTE FUNCTION identity_audit_append_only();
