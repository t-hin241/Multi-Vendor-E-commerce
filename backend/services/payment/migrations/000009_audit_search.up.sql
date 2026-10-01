-- ADM-01/04: audit rows carry the request id that caused them (searchable
-- correlation), are indexed for the paginated admin audit search and can
-- no longer be updated or deleted. Existing rows keep request_id NULL: the
-- id was never recorded, and no value is invented for them.
ALTER TABLE payment_admin_audit ADD COLUMN request_id TEXT CHECK (request_id IS NULL OR length(request_id) <= 64);
CREATE INDEX payment_audit_recent_idx ON payment_admin_audit (created_at DESC, id);
CREATE INDEX payment_audit_actor_idx ON payment_admin_audit (actor_id, created_at DESC);
CREATE INDEX payment_audit_request_idx ON payment_admin_audit (request_id) WHERE request_id IS NOT NULL;
CREATE FUNCTION payment_audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Audit rows are append-only';
END $$;
CREATE TRIGGER payment_audit_append_only BEFORE UPDATE OR DELETE ON payment_admin_audit
FOR EACH ROW EXECUTE FUNCTION payment_audit_append_only();
