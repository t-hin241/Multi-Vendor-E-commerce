ALTER TABLE cart_items ADD COLUMN seen_pricing_version bigint CHECK(seen_pricing_version>0);
-- Leave legacy references unknown: only explicit buyer confirmation fills them.
