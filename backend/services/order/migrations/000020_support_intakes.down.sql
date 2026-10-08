-- Refuse while buyers' intakes exist: they are support records. Turning
-- FEATURE_ORDER_SUPPORT_ENABLED off stops new intakes instead.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM support_intakes) THEN
        RAISE EXCEPTION 'support intakes exist; turn FEATURE_ORDER_SUPPORT_ENABLED off instead of migrating down';
    END IF;
END $$;

-- Fails while support_intake audit rows exist.
ALTER TABLE order_admin_audit DROP CONSTRAINT IF EXISTS order_admin_audit_entity_type_check;
ALTER TABLE order_admin_audit ADD CONSTRAINT order_admin_audit_entity_type_check
    CHECK (entity_type IN ('order', 'refund', 'return_request', 'effect', 'commission_rule', 'support_case'));

DROP TABLE support_intakes;
DROP FUNCTION support_intake_guard();
