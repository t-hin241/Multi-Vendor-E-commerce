-- AF-20: business reports read sub-orders by creation time, for all shops
-- or one, without scanning the whole history. Plain CREATE INDEX locks
-- writes on vendor_orders while it builds; run it in a quiet window on a
-- large table (see deploy/business-reports-runbook.md).
CREATE INDEX IF NOT EXISTS vendor_orders_created_idx ON vendor_orders (created_at, id);
CREATE INDEX IF NOT EXISTS vendor_orders_vendor_created_idx ON vendor_orders (vendor_id, created_at, id);
