-- Refuse while holds exist: dropping them would let payouts claim money a
-- support case still protects.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM settlement_holds) THEN
        RAISE EXCEPTION 'settlement holds exist; turn FEATURE_SETTLEMENT_HOLD_LEDGER_ENABLED off in Order instead of migrating down';
    END IF;
END $$;

DROP TABLE settlement_holds;
DROP FUNCTION settlement_hold_guard();
