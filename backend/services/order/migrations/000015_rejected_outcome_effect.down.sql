-- Fails while rejected-outcome effects exist; they must be delivered first.
ALTER TABLE order_effects DROP CONSTRAINT IF EXISTS order_effects_kind_check;
ALTER TABLE order_effects ADD CONSTRAINT order_effects_kind_check CHECK (kind IN (
    'create_shipment', 'cancel_shipment', 'release_inventory', 'notify', 'request_refund', 'restock_return', 'settle_vendor_order'));
