-- Refuse while returns use the return shipping workflow: their parcels,
-- receipts and deadlines would be lost. Turn FEATURE_RETURN_SHIPPING_ENABLED
-- off instead of migrating down.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM return_requests WHERE authorization_version > 0 OR shipping_status IS NOT NULL)
       OR EXISTS (SELECT 1 FROM return_goods_receipts) THEN
        RAISE EXCEPTION 'return shipping data exists; turn FEATURE_RETURN_SHIPPING_ENABLED off instead of migrating down';
    END IF;
END $$;

-- Fails while effects of the new kinds exist; they must be handled first.
ALTER TABLE order_effects DROP CONSTRAINT IF EXISTS order_effects_kind_check;
ALTER TABLE order_effects ADD CONSTRAINT order_effects_kind_check CHECK (kind IN (
    'create_shipment', 'cancel_shipment', 'release_inventory', 'notify', 'request_refund', 'restock_return', 'settle_vendor_order',
    'report_rejected_outcome', 'acquire_settlement_hold', 'release_settlement_hold', 'stop_fulfillment', 'recover_cancelled_stock',
    'create_replacement_attempt', 'recover_delivery_stock'));

DROP TABLE return_goods_receipts;
DROP INDEX return_requests_dispatch_due_idx;
ALTER TABLE return_requests
    DROP CONSTRAINT return_requests_dispatch_check,
    DROP CONSTRAINT return_requests_authorization_check,
    DROP COLUMN inspection_disputed, DROP COLUMN restock_quantity, DROP COLUMN dispatch_overdue_at, DROP COLUMN dispatch_hash,
    DROP COLUMN dispatch_key, DROP COLUMN dispatched_at, DROP COLUMN dispatch_tracking, DROP COLUMN dispatch_carrier,
    DROP COLUMN return_shipment_id, DROP COLUMN shipping_status, DROP COLUMN dispatch_deadline, DROP COLUMN return_fee_cap,
    DROP COLUMN return_fee_payer, DROP COLUMN return_destination, DROP COLUMN authorized_by, DROP COLUMN authorized_at,
    DROP COLUMN authorization_version;
