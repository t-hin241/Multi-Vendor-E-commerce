DROP TRIGGER payout_item_destination_immutable ON payout_items;
DROP FUNCTION protect_payout_item_destination();
ALTER TABLE payout_items DROP CONSTRAINT payout_destination_reference_required;
ALTER TABLE payout_items DROP COLUMN destination_account_id, DROP COLUMN destination_version, DROP COLUMN currency;
