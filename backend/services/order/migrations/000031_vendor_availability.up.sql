-- AF-18: checkout fences on the shop's selling version and on whether it
-- takes new orders (vacation). Existing rows default to accepting; Vendor's
-- next reconciliation (15 s) or event fills in the real value.
ALTER TABLE vendor_sale_status ADD COLUMN accepting_orders BOOLEAN NOT NULL DEFAULT TRUE;
