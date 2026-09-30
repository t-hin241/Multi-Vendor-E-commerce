CREATE TABLE payment_order_sync (
 payment_intent_id UUID PRIMARY KEY REFERENCES payment_intents(id),
 outcome TEXT NOT NULL CHECK(outcome IN ('captured','failed')),
 attempts INT NOT NULL DEFAULT 0,
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 delivered_at TIMESTAMPTZ,
 requires_review BOOLEAN NOT NULL DEFAULT false,
 last_error TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX payment_order_sync_due_idx ON payment_order_sync(next_attempt_at) WHERE delivered_at IS NULL AND NOT requires_review;
CREATE FUNCTION payment_queue_order_sync() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status IN ('captured','failed') AND NEW.status IS DISTINCT FROM OLD.status THEN
  INSERT INTO payment_order_sync(payment_intent_id,outcome) VALUES(NEW.id,NEW.status)
  ON CONFLICT(payment_intent_id) DO UPDATE SET outcome=excluded.outcome,attempts=0,next_attempt_at=now(),delivered_at=NULL,requires_review=false,last_error=NULL;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER payment_inventory_sync AFTER UPDATE OF status ON payment_intents FOR EACH ROW EXECUTE FUNCTION payment_queue_order_sync();
