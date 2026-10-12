-- Losing a differentiated price/audit is unsafe even when current prices match.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM variant_price_audit WHERE source<>'product_backfill_v1') OR
    EXISTS(SELECT 1 FROM product_variants v JOIN products p ON p.id=v.product_id WHERE v.price_amount<>p.price_amount)
 THEN RAISE EXCEPTION 'Variant pricing has been used; keep compatible readers and disable new checkout instead'; END IF;
END $$;
DROP TRIGGER catalog_variant_price_fence ON product_variants;
DROP FUNCTION catalog_variant_price_fence();
DROP TRIGGER catalog_variant_price_defaults ON product_variants;
DROP FUNCTION catalog_variant_price_defaults();
DROP TABLE variant_price_outbox;
DROP TABLE variant_price_audit;
DROP FUNCTION variant_price_audit_immutable();
ALTER TABLE product_variants DROP COLUMN price_amount,DROP COLUMN price_version,DROP COLUMN price_source;
ALTER TABLE product_audit_logs DROP CONSTRAINT product_audit_logs_action_check;
-- Existing replay records remain valid history; old writers still cannot add
-- the variant-price action after a safe rollback.
ALTER TABLE product_audit_logs ADD CONSTRAINT product_audit_logs_action_check
 CHECK(action IN('approved','rejected','content_revision','replay_status','replay_cleanup')) NOT VALID;
