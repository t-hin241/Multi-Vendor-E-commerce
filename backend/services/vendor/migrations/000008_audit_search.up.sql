-- ADM-01/04: audit rows carry the request id that caused them (searchable
-- correlation), are indexed for the paginated admin audit search and can
-- no longer be updated or deleted. Existing rows keep request_id NULL: the
-- id was never recorded, and no value is invented for them.
ALTER TABLE vendor_audit_logs ADD COLUMN request_id TEXT CHECK (request_id IS NULL OR length(request_id) <= 64);
CREATE INDEX vendor_audit_recent_idx ON vendor_audit_logs (created_at DESC, id);
CREATE INDEX vendor_audit_actor_idx ON vendor_audit_logs (actor_user_id, created_at DESC);
CREATE INDEX vendor_audit_request_idx ON vendor_audit_logs (request_id) WHERE request_id IS NOT NULL;
ALTER TABLE vendor_audit_logs DROP CONSTRAINT vendor_audit_logs_action_check;
ALTER TABLE vendor_audit_logs ADD CONSTRAINT vendor_audit_logs_action_check CHECK(action IN
 ('approved','rejected','suspended','resubmitted','restored','payout_submitted','payout_verified','payout_rejected','payout_disabled','payout_details_read','event_replayed'));
CREATE FUNCTION vendor_audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Audit rows are append-only';
END $$;
CREATE TRIGGER vendor_audit_append_only BEFORE UPDATE OR DELETE ON vendor_audit_logs
FOR EACH ROW EXECUTE FUNCTION vendor_audit_append_only();
