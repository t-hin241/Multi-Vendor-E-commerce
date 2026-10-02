-- PLT-03: when Order refuses a payment or refund outcome that arrived as an
-- event, it tells Payment through a durable effect
-- (order.payment_outcome_rejected), as the HTTP refusal used to.
ALTER TABLE order_effects DROP CONSTRAINT IF EXISTS order_effects_kind_check;
ALTER TABLE order_effects ADD CONSTRAINT order_effects_kind_check CHECK (kind IN (
    'create_shipment', 'cancel_shipment', 'release_inventory', 'notify', 'request_refund', 'restock_return', 'settle_vendor_order',
    'report_rejected_outcome'));
