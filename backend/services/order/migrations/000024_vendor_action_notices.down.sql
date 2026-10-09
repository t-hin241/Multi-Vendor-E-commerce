-- Fails while notify_vendor effects exist (sent or not): turn
-- FEATURE_VENDOR_ACTION_NOTICES_ENABLED off instead of migrating down.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM order_effects WHERE kind = 'notify_vendor') THEN
        RAISE EXCEPTION 'vendor notice effects exist; turn FEATURE_VENDOR_ACTION_NOTICES_ENABLED off instead of migrating down';
    END IF;
END $$;
ALTER TABLE order_effects DROP CONSTRAINT IF EXISTS order_effects_kind_check;
ALTER TABLE order_effects ADD CONSTRAINT order_effects_kind_check CHECK (kind IN (
    'create_shipment', 'cancel_shipment', 'release_inventory', 'notify', 'request_refund', 'restock_return', 'settle_vendor_order',
    'report_rejected_outcome', 'acquire_settlement_hold', 'release_settlement_hold', 'stop_fulfillment', 'recover_cancelled_stock',
    'create_replacement_attempt', 'recover_delivery_stock', 'authorize_return_shipment', 'dispatch_return_shipment',
    'close_return_shipment'));
