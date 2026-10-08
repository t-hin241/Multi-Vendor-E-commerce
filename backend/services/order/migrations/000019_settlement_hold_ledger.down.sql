-- Refuse while cases name holds in Payment: dropping the columns would lose
-- which hold to release. Turn FEATURE_SETTLEMENT_HOLD_LEDGER_ENABLED off
-- instead (open holds are still released when their cases close).
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM support_cases WHERE hold_id IS NOT NULL) THEN
        RAISE EXCEPTION 'support cases hold settlement holds; turn FEATURE_SETTLEMENT_HOLD_LEDGER_ENABLED off instead of migrating down';
    END IF;
END $$;

-- Fails while hold effects exist; they must be delivered first.
ALTER TABLE order_effects DROP CONSTRAINT IF EXISTS order_effects_kind_check;
ALTER TABLE order_effects ADD CONSTRAINT order_effects_kind_check CHECK (kind IN (
    'create_shipment', 'cancel_shipment', 'release_inventory', 'notify', 'request_refund', 'restock_return', 'settle_vendor_order',
    'report_rejected_outcome'));

DROP INDEX support_cases_hold_missing_idx;
DROP INDEX support_cases_hold_attention_idx;
ALTER TABLE support_cases DROP CONSTRAINT support_cases_hold_pair,
    DROP COLUMN hold_updated_at, DROP COLUMN hold_note, DROP COLUMN hold_status, DROP COLUMN hold_id;
