ALTER TABLE products ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK(version > 0);
ALTER TABLE products ADD COLUMN enforced_version BIGINT NOT NULL DEFAULT 0;

CREATE TABLE product_status_outbox (
 product_id UUID PRIMARY KEY REFERENCES products(id),
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 attempts INT NOT NULL DEFAULT 0,
 last_error TEXT
);

CREATE FUNCTION catalog_product_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF (NEW.name,NEW.description,NEW.price_amount,NEW.currency,NEW.category_id,NEW.status,NEW.is_active)
     IS NOT DISTINCT FROM (OLD.name,OLD.description,OLD.price_amount,OLD.currency,OLD.category_id,OLD.status,OLD.is_active)
  THEN RETURN NEW; END IF;
  NEW.version=OLD.version+1;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER catalog_product_version BEFORE UPDATE ON products FOR EACH ROW EXECUTE FUNCTION catalog_product_changed();

CREATE FUNCTION catalog_enqueue_product() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' OR NEW.version IS DISTINCT FROM OLD.version THEN
  INSERT INTO product_status_outbox(product_id) VALUES(NEW.id)
  ON CONFLICT(product_id) DO UPDATE SET next_attempt_at=now(),attempts=0,last_error=NULL;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER catalog_product_outbox AFTER INSERT OR UPDATE ON products FOR EACH ROW EXECUTE FUNCTION catalog_enqueue_product();

CREATE TABLE catalog_object_cleanup (
 object_key TEXT PRIMARY KEY,
 product_id UUID NOT NULL REFERENCES products(id),
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now()+interval '24 hours',
 attempts INT NOT NULL DEFAULT 0,
 last_error TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX catalog_object_cleanup_due_idx ON catalog_object_cleanup(next_attempt_at) WHERE attempts < 10;

ALTER TABLE attribute_options ADD CONSTRAINT attribute_options_id_attribute_unique UNIQUE(id,attribute_id);
ALTER TABLE product_variant_options ADD CONSTRAINT variant_option_attribute_fk
 FOREIGN KEY(option_id,attribute_id) REFERENCES attribute_options(id,attribute_id) NOT VALID;
ALTER TABLE product_attribute_values ADD CONSTRAINT value_option_attribute_fk
 FOREIGN KEY(option_id,attribute_id) REFERENCES attribute_options(id,attribute_id) NOT VALID;
ALTER TABLE product_attribute_values ADD CONSTRAINT value_exactly_one CHECK(num_nonnulls(option_id,value_text,value_number,value_boolean)=1) NOT VALID;
ALTER TABLE product_variants ADD CONSTRAINT variant_sku_nonempty CHECK(length(btrim(sku)) BETWEEN 1 AND 100) NOT VALID;
