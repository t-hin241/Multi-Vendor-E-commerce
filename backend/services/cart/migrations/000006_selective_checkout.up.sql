-- Legacy receipts already contain the frozen all-cart lines. Never rebuild them.
ALTER TABLE cart_checkout_operations ADD COLUMN selection_hash text NOT NULL DEFAULT '';
ALTER TABLE cart_checkout_operations ADD CONSTRAINT cart_selection_hash_format
    CHECK (selection_hash = '' OR selection_hash ~ '^[0-9a-f]{64}$');

CREATE FUNCTION protect_cart_checkout_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF ROW(NEW.operation_id, NEW.buyer_id, NEW.cart_id, NEW.cart_version, NEW.lines, NEW.selection_hash)
       IS DISTINCT FROM
       ROW(OLD.operation_id, OLD.buyer_id, OLD.cart_id, OLD.cart_version, OLD.lines, OLD.selection_hash) THEN
        RAISE EXCEPTION 'checkout snapshot is immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER cart_checkout_snapshot_immutable BEFORE UPDATE ON cart_checkout_operations
    FOR EACH ROW EXECUTE FUNCTION protect_cart_checkout_snapshot();
