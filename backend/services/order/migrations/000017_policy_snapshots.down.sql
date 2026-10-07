-- Refuses to drop the policy snapshots of orders: they record what the
-- buyer agreed to. Roll back by turning FEATURE_VERSIONED_POLICIES_ENABLED off.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM orders WHERE policy_snapshot IS NOT NULL)
    OR EXISTS (SELECT 1 FROM vendor_orders WHERE policy_snapshot IS NOT NULL) THEN
  RAISE EXCEPTION 'orders carry policy snapshots; keep migration 000017 and disable FEATURE_VERSIONED_POLICIES_ENABLED';
 END IF;
END $$;
ALTER TABLE vendor_orders DROP COLUMN IF EXISTS policy_snapshot;
ALTER TABLE orders DROP COLUMN IF EXISTS policy_snapshot;
DROP TABLE IF EXISTS policy_versions;
DROP FUNCTION IF EXISTS policy_versions_immutable();
