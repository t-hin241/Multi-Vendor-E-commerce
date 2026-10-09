-- Refuses while a return or refund hold is not released: dropping it would
-- forget a hold Payment still keeps. Turn the ledger flag off and let the
-- sweep release them first.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM source_settlement_holds WHERE status <> 'released') THEN
        RAISE EXCEPTION 'return/refund settlement holds are open; release them before migrating down';
    END IF;
END $$;
DROP TABLE source_settlement_holds;
