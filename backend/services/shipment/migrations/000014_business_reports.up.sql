-- AF-20: business reports read first delivery attempts by the time they
-- became fulfillable, for all shops or one. Plain CREATE INDEX locks writes
-- on shipments while it builds; use a quiet window on a large table.
CREATE INDEX IF NOT EXISTS shipments_report_created_idx ON shipments (created_at, id) WHERE attempt_no = 1;
CREATE INDEX IF NOT EXISTS shipments_report_vendor_created_idx ON shipments (vendor_id, created_at, id) WHERE attempt_no = 1;
