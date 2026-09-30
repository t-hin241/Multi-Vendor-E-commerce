-- Loses receipts and audit. Fails if an intent is still 'creating' or
-- 'expired', or has no provider id: those must be reconciled first.
DROP TABLE IF EXISTS payment_admin_audit;
DROP TABLE IF EXISTS payment_receipts;
DROP SEQUENCE IF EXISTS payment_provider_order_code_seq;
DROP INDEX IF EXISTS payment_intents_reconcile_idx;
DROP INDEX IF EXISTS payment_intents_open_order_key;
DROP INDEX IF EXISTS payment_intents_provider_reference_key;
ALTER TABLE payment_intents
    DROP COLUMN IF EXISTS closed_reason,
    DROP COLUMN IF EXISTS last_checked_at,
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS create_attempts,
    DROP COLUMN IF EXISTS provider_reference;
ALTER TABLE payment_intents ALTER COLUMN provider_intent_id SET NOT NULL;
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_status_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_status_check CHECK (
    status IN ('pending', 'authorized', 'captured', 'failed', 'refunded'));
