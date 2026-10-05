-- Storefront listing (ListStorefront) pages through visible products in a
-- fixed order. Without an index on that order every page read and sorted
-- all visible products (load test, docs/module-details/17-observability-capacity.md);
-- with it a page reads about offset + limit rows in order. Partial on the
-- visibility condition the listing always applies. Plain CREATE INDEX:
-- products is small at this stage; on a large table build it CONCURRENTLY
-- by hand before this migration runs.
CREATE INDEX IF NOT EXISTS products_storefront_recent_idx
    ON products (created_at DESC, id DESC) WHERE status = 'approved' AND is_active;
-- price_asc and price_desc (one btree serves both directions).
CREATE INDEX IF NOT EXISTS products_storefront_price_idx
    ON products (price_amount, id) WHERE status = 'approved' AND is_active;
