-- Refuses while a notice was not relayed yet: it would be lost. Let the
-- worker drain the outbox first.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM order_buyer_notices WHERE delivered_at IS NULL) THEN
        RAISE EXCEPTION 'undelivered buyer notices exist; let the worker drain them first';
    END IF;
END $$;
DROP TABLE order_buyer_notices;
