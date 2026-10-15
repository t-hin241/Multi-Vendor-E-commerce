-- AF-20: captured money is reported by when the capture was recorded.
-- payment_intents had no such time; from now on it is set once, when the
-- status first becomes captured (a late capture after a failure counts at
-- its own time). History is backfilled from the first captured outcome
-- queued for Order, else the row's last update, and flagged estimated.
ALTER TABLE payment_intents
    ADD COLUMN captured_at TIMESTAMPTZ,
    ADD COLUMN captured_at_estimated BOOLEAN NOT NULL DEFAULT FALSE;

CREATE FUNCTION payment_intents_captured_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'captured' AND OLD.status IS DISTINCT FROM 'captured' AND NEW.captured_at IS NULL THEN
        NEW.captured_at := now();
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER payment_intents_captured_at BEFORE UPDATE OF status ON payment_intents
    FOR EACH ROW EXECUTE FUNCTION payment_intents_captured_at();

UPDATE payment_intents p SET captured_at = s.created_at, captured_at_estimated = TRUE
FROM payment_order_sync s
WHERE s.payment_intent_id = p.id AND s.outcome = 'captured' AND p.captured_at IS NULL;
-- A refunded intent was captured first; without a sync row use updated_at.
UPDATE payment_intents SET captured_at = updated_at, captured_at_estimated = TRUE
WHERE captured_at IS NULL AND status IN ('captured', 'refunded');

CREATE INDEX payment_intents_captured_idx ON payment_intents (captured_at, id) WHERE captured_at IS NOT NULL;
-- A succeeded refund counts when it was confirmed (resolved_at; older rows
-- without it: their last update).
CREATE INDEX payment_refunds_resolved_idx ON payment_refunds ((COALESCE(resolved_at, updated_at)), id) WHERE status = 'succeeded';
