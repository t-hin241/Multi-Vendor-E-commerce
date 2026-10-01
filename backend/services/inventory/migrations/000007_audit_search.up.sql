-- ADM-01/04: audit rows carry the request id that caused them (searchable
-- correlation), are indexed for the paginated admin audit search and can
-- no longer be updated or deleted. Existing rows keep request_id NULL: the
-- id was never recorded, and no value is invented for them.
-- The table also records restock decisions from now on, so a row names its
-- entity instead of always pointing at a reservation operation.
ALTER TABLE inventory_operation_audit ALTER COLUMN order_id DROP NOT NULL;
-- An approval carries no reason; a rejection or repair still does.
ALTER TABLE inventory_operation_audit ALTER COLUMN reason DROP NOT NULL;
ALTER TABLE inventory_operation_audit ADD COLUMN entity_type TEXT NOT NULL DEFAULT 'reservation_operation';
ALTER TABLE inventory_operation_audit ADD COLUMN entity_id TEXT;
ALTER TABLE inventory_operation_audit ADD COLUMN changes JSONB;
UPDATE inventory_operation_audit SET entity_id = order_id::text WHERE entity_id IS NULL;
ALTER TABLE inventory_operation_audit ADD CONSTRAINT inventory_operation_audit_entity_check
 CHECK (order_id IS NOT NULL OR entity_id IS NOT NULL);
ALTER TABLE inventory_operation_audit ADD COLUMN request_id TEXT CHECK (request_id IS NULL OR length(request_id) <= 64);
CREATE INDEX inventory_audit_recent_idx ON inventory_operation_audit (created_at DESC, id);
CREATE INDEX inventory_audit_actor_idx ON inventory_operation_audit (actor_user_id, created_at DESC);
CREATE INDEX inventory_audit_request_idx ON inventory_operation_audit (request_id) WHERE request_id IS NOT NULL;
CREATE INDEX inventory_audit_entity_idx ON inventory_operation_audit (entity_type, entity_id);
CREATE FUNCTION inventory_audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Audit rows are append-only';
END $$;
CREATE TRIGGER inventory_audit_append_only BEFORE UPDATE OR DELETE ON inventory_operation_audit
FOR EACH ROW EXECUTE FUNCTION inventory_audit_append_only();
