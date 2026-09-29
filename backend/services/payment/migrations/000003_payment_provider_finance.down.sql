DROP TABLE IF EXISTS payout_items;
DROP TABLE IF EXISTS payout_batches;
DROP TABLE IF EXISTS payment_refunds;
ALTER TABLE payment_intents DROP COLUMN IF EXISTS expires_at;
ALTER TABLE payment_intents DROP COLUMN IF EXISTS qr_code;
ALTER TABLE payment_intents DROP COLUMN IF EXISTS checkout_url;
