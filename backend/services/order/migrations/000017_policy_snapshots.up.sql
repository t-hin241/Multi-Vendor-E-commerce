-- AF-02: Order keeps a read model of published policy versions (from
-- vendor.policy_published) and snapshots, on every new order and vendor
-- order, the versions in force at checkout and the rules it enforces for
-- them (return window, return shipping refund). The snapshot follows the
-- order's created_at, never the date of a later return or refund.
-- Orders placed before have no snapshot (NULL): they keep the legacy rules
-- (return window from ORDER_RETURN_WINDOW_DAYS, shipping not refunded);
-- nothing is backfilled or guessed.

CREATE TABLE policy_versions (
    policy_id UUID PRIMARY KEY,
    scope TEXT NOT NULL CHECK (scope IN ('marketplace', 'shop')),
    vendor_id UUID,
    kind TEXT NOT NULL CHECK (length(kind) BETWEEN 1 AND 40),
    version BIGINT NOT NULL CHECK (version > 0),
    content_hash TEXT NOT NULL CHECK (length(content_hash) BETWEEN 1 AND 128),
    rule_refs JSONB NOT NULL DEFAULT '{}'::jsonb,
    effective_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((scope = 'shop') = (vendor_id IS NOT NULL))
);
CREATE INDEX policy_versions_marketplace_idx ON policy_versions (kind, effective_at DESC) WHERE scope = 'marketplace';
CREATE INDEX policy_versions_shop_idx ON policy_versions (vendor_id, effective_at DESC) WHERE scope = 'shop';

-- Versions are immutable facts: the read model never changes a row.
CREATE FUNCTION policy_versions_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Published policy versions are immutable';
END $$;
CREATE TRIGGER policy_versions_immutable BEFORE UPDATE OR DELETE ON policy_versions
FOR EACH ROW EXECUTE FUNCTION policy_versions_immutable();

ALTER TABLE orders ADD COLUMN policy_snapshot JSONB;
ALTER TABLE vendor_orders ADD COLUMN policy_snapshot JSONB;
