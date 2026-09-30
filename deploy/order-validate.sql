-- Order upgrade validation. Read only; run against order_db after
-- migrations 000010/000011. Every count must be 0.
SELECT 'orders_missing_subtotal' AS check_name, count(*) AS violations FROM orders WHERE subtotal_amount IS NULL
UNION ALL SELECT 'commission_rules_missing_version', count(*) FROM commission_rules WHERE version IS NULL
UNION ALL SELECT 'legacy_commission_unlabelled', count(*) FROM vendor_orders
  WHERE commission_rate_bps IS NOT NULL AND commission_source IS NULL
UNION ALL SELECT 'completed_without_completed_at', count(*) FROM vendor_orders
  WHERE status = 'completed' AND completed_at IS NULL
UNION ALL SELECT 'returns_missing_quantity', count(*) FROM return_requests
  WHERE quantity IS NULL OR refund_amount IS NULL OR policy_version IS NULL
UNION ALL SELECT 'returns_without_audit', count(*) FROM return_requests rr
  WHERE NOT EXISTS (SELECT 1 FROM return_request_events e WHERE e.return_request_id = rr.id)
UNION ALL SELECT 'returns_over_purchased', count(*) FROM
  (SELECT rr.order_item_id FROM return_requests rr JOIN order_items oi ON oi.id = rr.order_item_id
   WHERE rr.status <> 'rejected' GROUP BY rr.order_item_id, oi.quantity HAVING sum(rr.quantity) > oi.quantity) d
UNION ALL SELECT 'refunded_over_total', count(*) FROM orders WHERE refunded_amount > total_amount;
