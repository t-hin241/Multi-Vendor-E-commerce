ALTER TABLE payout_items ADD COLUMN destination_account_id UUID,
 ADD COLUMN destination_version BIGINT,
 ADD COLUMN currency TEXT;
ALTER TABLE payout_items ADD CONSTRAINT payout_destination_reference_required
 CHECK(destination_account_id IS NOT NULL AND destination_version IS NOT NULL AND destination_version>0 AND currency IS NOT NULL AND currency ~ '^[A-Z]{3}$') NOT VALID;

CREATE FUNCTION protect_payout_item_destination() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.vendor_id,NEW.destination_account_id,NEW.destination_version,NEW.currency,NEW.amount)
 IS DISTINCT FROM (OLD.vendor_id,OLD.destination_account_id,OLD.destination_version,OLD.currency,OLD.amount) THEN
  RAISE EXCEPTION 'Payout destination and amount are immutable; review the batch';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER payout_item_destination_immutable BEFORE UPDATE ON payout_items
FOR EACH ROW EXECUTE FUNCTION protect_payout_item_destination();
