-- Refuses while a notice was not relayed yet: it would be lost. Turn
-- FEATURE_MANUAL_REFUND_WORKFLOW_ENABLED off and let the relay drain first.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM payment_buyer_notices WHERE delivered_at IS NULL) THEN
        RAISE EXCEPTION 'undelivered buyer notices exist; let the relay drain them first';
    END IF;
END $$;
DROP TABLE payment_buyer_notices;
