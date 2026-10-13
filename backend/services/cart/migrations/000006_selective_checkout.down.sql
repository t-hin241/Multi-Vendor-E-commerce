DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM cart_checkout_operations WHERE selection_hash <> '') THEN
        RAISE EXCEPTION 'selective checkout receipts exist; disable new selections and retain compatible readers';
    END IF;
END $$;
DROP TRIGGER cart_checkout_snapshot_immutable ON cart_checkout_operations;
DROP FUNCTION protect_cart_checkout_snapshot();
ALTER TABLE cart_checkout_operations DROP COLUMN selection_hash;
