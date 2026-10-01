-- Shipment upgrade preflight (SHP-01..05). Read only.

-- ===== shipment_db (before migration 000005) =====
-- Informational: how existing shipments enter the new flow.
--  * shipped_without_tracking: cannot be followed by the buyer; record the
--    tracking number from the admin Fulfillment page after deploy.
--  * in_transit / interceptions_open: carried over as they are. Shipment
--    sends no event for them retroactively (Order already holds the status
--    the vendor set by hand under the old flow).
--  * final_with_contact_details: removed by the retention job once older
--    than SHIPMENT_ADDRESS_RETENTION_DAYS (default 180).
SELECT 'shipped_without_tracking' AS check_name, count(*) AS rows FROM shipments WHERE status = 'shipped' AND tracking_number IS NULL
UNION ALL SELECT 'pending_or_ready', count(*) FROM shipments WHERE status IN ('pending', 'ready_to_ship')
UNION ALL SELECT 'in_transit', count(*) FROM shipments WHERE status = 'shipped'
UNION ALL SELECT 'interceptions_open', count(*) FROM shipments WHERE status = 'interception_requested'
UNION ALL SELECT 'final_with_contact_details_older_than_180_days', count(*) FROM shipments
  WHERE status IN ('delivered', 'cancelled') AND updated_at < now() - interval '180 days' AND phone IS NOT NULL;

-- ===== catalog_db =====
-- After the upgrade a shop with a product lacking a package weight is shown
-- as "shipping unavailable" at checkout instead of being charged a guessed
-- fee. Fill in the weight of these approved, active products first.
SELECT p.vendor_id, count(*) AS products_without_weight FROM products p
LEFT JOIN product_packaging pp ON pp.product_id = p.id
WHERE p.status = 'approved' AND p.is_active AND (pp.weight_grams IS NULL OR pp.weight_grams <= 0)
GROUP BY p.vendor_id ORDER BY 2 DESC;
