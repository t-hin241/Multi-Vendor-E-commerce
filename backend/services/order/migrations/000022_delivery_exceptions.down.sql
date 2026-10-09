-- Refuse while delivery exceptions exist: they hold payouts, own refunds
-- and redelivery attempts. Turn FEATURE_DELIVERY_RESOLUTION_ENABLED off
-- (Order and Shipment) instead of migrating down.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM delivery_exceptions) THEN
        RAISE EXCEPTION 'delivery exceptions exist; turn FEATURE_DELIVERY_RESOLUTION_ENABLED off instead of migrating down';
    END IF;
END $$;

-- These fail while rows of the new kinds exist; they must be handled first.
ALTER TABLE order_admin_audit DROP CONSTRAINT IF EXISTS order_admin_audit_entity_type_check;
ALTER TABLE order_admin_audit ADD CONSTRAINT order_admin_audit_entity_type_check
    CHECK (entity_type IN ('order', 'refund', 'return_request', 'effect', 'commission_rule', 'support_case', 'support_intake',
        'cancellation_request'));
ALTER TABLE order_effects DROP CONSTRAINT IF EXISTS order_effects_kind_check;
ALTER TABLE order_effects ADD CONSTRAINT order_effects_kind_check CHECK (kind IN (
    'create_shipment', 'cancel_shipment', 'release_inventory', 'notify', 'request_refund', 'restock_return', 'settle_vendor_order',
    'report_rejected_outcome', 'acquire_settlement_hold', 'release_settlement_hold', 'stop_fulfillment', 'recover_cancelled_stock'));
ALTER TABLE order_refunds DROP CONSTRAINT IF EXISTS order_refunds_reason_code_check;
ALTER TABLE order_refunds ADD CONSTRAINT order_refunds_reason_code_check
    CHECK (reason_code IN ('return', 'dispute', 'late_payment', 'duplicate_payment', 'cancellation'));

DROP TABLE delivery_exception_receipt_lines;
DROP TABLE delivery_exception_receipts;
DROP TABLE delivery_exception_history;
DROP TABLE delivery_exceptions;
DROP FUNCTION delivery_exception_guard();
