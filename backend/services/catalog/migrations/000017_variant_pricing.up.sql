-- Additive cutover: persist the price used by legacy readers before enabling
-- differentiated prices. The capability flag stays off until consumers deploy.
ALTER TABLE product_variants ADD COLUMN price_amount bigint;
ALTER TABLE product_variants ADD COLUMN price_version bigint NOT NULL DEFAULT 1 CHECK(price_version>0);
ALTER TABLE product_variants ADD COLUMN price_source text NOT NULL DEFAULT 'product_backfill_v1';
UPDATE product_variants v SET price_amount=p.price_amount FROM products p WHERE p.id=v.product_id;
ALTER TABLE product_variants ALTER COLUMN price_amount SET NOT NULL;
ALTER TABLE product_variants ADD CONSTRAINT variant_positive_price CHECK(price_amount>0);
ALTER TABLE product_audit_logs DROP CONSTRAINT product_audit_logs_action_check;
ALTER TABLE product_audit_logs ADD CONSTRAINT product_audit_logs_action_check CHECK(action IN('approved','rejected','content_revision','replay_status','replay_cleanup','replay_variant_price'));

CREATE TABLE variant_price_audit (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 product_id uuid NOT NULL REFERENCES products(id),
 variant_id uuid NOT NULL REFERENCES product_variants(id),
 actor_user_id uuid,
 old_price bigint,
 new_price bigint NOT NULL CHECK(new_price>0),
 price_version bigint NOT NULL CHECK(price_version>0),
 source text NOT NULL,
 request_id text,
 membership_version bigint CHECK(membership_version>0),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(variant_id,price_version)
);
CREATE TABLE variant_price_outbox (
 variant_id uuid NOT NULL REFERENCES product_variants(id),
 price_version bigint NOT NULL,
 product_id uuid NOT NULL REFERENCES products(id),
 product_version bigint NOT NULL,
 request_id text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 attempts integer NOT NULL DEFAULT 0,
 PRIMARY KEY(variant_id,price_version)
);
INSERT INTO variant_price_audit(product_id,variant_id,new_price,price_version,source)
 SELECT product_id,id,price_amount,price_version,'product_backfill_v1' FROM product_variants;
CREATE FUNCTION variant_price_audit_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'Variant price audit is append-only'; END $$;
CREATE TRIGGER variant_price_audit_immutable BEFORE UPDATE OR DELETE ON variant_price_audit
 FOR EACH ROW EXECUTE FUNCTION variant_price_audit_immutable();

-- Legacy INSERTs initialize a stored price; quote readers NEVER fall back.
CREATE FUNCTION catalog_variant_price_defaults() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.price_amount IS NULL THEN SELECT price_amount INTO NEW.price_amount FROM products WHERE id=NEW.product_id; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER catalog_variant_price_defaults BEFORE INSERT ON product_variants
 FOR EACH ROW EXECUTE FUNCTION catalog_variant_price_defaults();

-- Version the parent too: Order's existing product-sale fence covers every SKU.
CREATE FUNCTION catalog_variant_price_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND NEW.price_source='product_backfill_v1' THEN
  INSERT INTO variant_price_audit(product_id,variant_id,new_price,price_version,source)
  VALUES(NEW.product_id,NEW.id,NEW.price_amount,NEW.price_version,NEW.price_source);
 END IF;
 IF TG_OP='INSERT' OR NEW.price_amount IS DISTINCT FROM OLD.price_amount THEN
  UPDATE products SET version=version+1,updated_at=now() WHERE id=NEW.product_id;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER catalog_variant_price_fence AFTER INSERT OR UPDATE OF price_amount ON product_variants
 FOR EACH ROW EXECUTE FUNCTION catalog_variant_price_fence();
