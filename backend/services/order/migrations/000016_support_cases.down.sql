-- Refuses to drop support cases once any exist: rolling back an image
-- never deletes a buyer's case, its messages or the payout holds they keep.
-- Roll back by turning FEATURE_ORDER_SUPPORT_ENABLED off instead.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM support_cases) THEN
  RAISE EXCEPTION 'support_cases holds cases; keep migration 000016 and disable FEATURE_ORDER_SUPPORT_ENABLED';
 END IF;
 IF EXISTS (SELECT 1 FROM order_admin_audit WHERE entity_type = 'support_case') THEN
  RAISE EXCEPTION 'order_admin_audit holds support case audit; keep migration 000016';
 END IF;
END $$;
ALTER TABLE order_admin_audit DROP CONSTRAINT IF EXISTS order_admin_audit_entity_type_check;
ALTER TABLE order_admin_audit ADD CONSTRAINT order_admin_audit_entity_type_check
    CHECK (entity_type IN ('order', 'refund', 'return_request', 'effect', 'commission_rule'));
DROP TABLE IF EXISTS support_case_history;
DROP TABLE IF EXISTS case_attachments;
DROP TABLE IF EXISTS support_messages;
DROP TABLE IF EXISTS support_cases;
DROP FUNCTION IF EXISTS support_append_only();
