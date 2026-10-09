-- PW-001: an operator may cancel a payout item that was claimed but not
-- transferred (e.g. a settlement hold arrived after the claim and was
-- flagged payout_already_claimed). The item's entries go back to unpaid,
-- like a failed transfer, but the record says no transfer was attempted.
ALTER TABLE payout_items DROP CONSTRAINT IF EXISTS payout_items_status_check;
ALTER TABLE payout_items ADD CONSTRAINT payout_items_status_check CHECK (status IN ('pending', 'succeeded', 'failed', 'cancelled'));
