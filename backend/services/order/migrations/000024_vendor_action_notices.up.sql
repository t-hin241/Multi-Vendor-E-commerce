-- AF-08: notify_vendor effects tell a shop it has work (new paid order,
-- cancellation or return request, return parcel on its way); Notification
-- decides who in the shop is told.
ALTER TABLE order_effects DROP CONSTRAINT IF EXISTS order_effects_kind_check;
ALTER TABLE order_effects ADD CONSTRAINT order_effects_kind_check CHECK (kind IN (
    'create_shipment', 'cancel_shipment', 'release_inventory', 'notify', 'request_refund', 'restock_return', 'settle_vendor_order',
    'report_rejected_outcome', 'acquire_settlement_hold', 'release_settlement_hold', 'stop_fulfillment', 'recover_cancelled_stock',
    'create_replacement_attempt', 'recover_delivery_stock', 'authorize_return_shipment', 'dispatch_return_shipment',
    'close_return_shipment', 'notify_vendor'));
