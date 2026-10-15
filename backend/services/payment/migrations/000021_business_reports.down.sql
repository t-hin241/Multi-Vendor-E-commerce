DROP INDEX IF EXISTS payment_refunds_resolved_idx;
DROP INDEX IF EXISTS payment_intents_captured_idx;
DROP TRIGGER IF EXISTS payment_intents_captured_at ON payment_intents;
DROP FUNCTION IF EXISTS payment_intents_captured_at();
ALTER TABLE payment_intents DROP COLUMN IF EXISTS captured_at_estimated, DROP COLUMN IF EXISTS captured_at;
