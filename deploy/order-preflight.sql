-- Order upgrade preflight (ORD-01..06). Read only. Run the first part
-- against order_db and the second against payment_db before Order
-- migrations 000010/000011 and Payment migration 000006.

-- ===== order_db =====
-- Blocking: must be 0, or have a reviewed fix, before migrating.
SELECT 'orders_without_vendor_orders' AS check_name, count(*) AS violations
  FROM orders o WHERE NOT EXISTS (SELECT 1 FROM vendor_orders v WHERE v.order_id = o.id)
UNION ALL SELECT 'return_items_missing', count(*)
  FROM return_requests rr LEFT JOIN order_items oi ON oi.id = rr.order_item_id WHERE oi.id IS NULL
UNION ALL SELECT 'commission_rules_same_created_at', count(*) FROM
  (SELECT created_at FROM commission_rules GROUP BY created_at HAVING count(*) > 1) d;

-- Informational: legacy data the new flow treats differently. Review each
-- group; none of it is rewritten into history the system never recorded.
--  * refunded_orders_without_receipt: set by the old admin "refunded"
--    transition, no Payment refund exists. refunded_amount stays 0; money
--    must be reconciled with the provider/bank by hand.
--  * paid_vendor_orders_without_commission: paid before commission was
--    snapshotted; they stay without a snapshot (commission_source NULL).
--  * legacy_returns_awaiting_refund: 'approved_awaiting_provider_refund'
--    becomes 'approved' (goods not received, nothing refunded).
--  * total_mismatch: orders whose total differs from its packages' items +
--    shipping; subtotal_amount is backfilled from the packages.
--  * completed_packages: completed_at is backfilled from updated_at, which
--    starts their return window; review before shortening the window.
SELECT 'refunded_orders_without_receipt' AS check_name, count(*) AS rows FROM orders WHERE status = 'refunded'
UNION ALL SELECT 'paid_vendor_orders_without_commission', count(*) FROM vendor_orders
  WHERE status IN ('paid', 'processing', 'shipped', 'completed') AND commission_rate_bps IS NULL
UNION ALL SELECT 'legacy_returns_awaiting_refund', count(*) FROM return_requests
  WHERE status = 'approved_awaiting_provider_refund'
UNION ALL SELECT 'total_mismatch', count(*) FROM orders o
  JOIN (SELECT order_id, sum(subtotal_amount + shipping_fee_amount) AS total FROM vendor_orders GROUP BY order_id) v
    ON v.order_id = o.id WHERE v.total <> o.total_amount
UNION ALL SELECT 'completed_packages', count(*) FROM vendor_orders WHERE status = 'completed'
UNION ALL SELECT 'pending_payment_orders', count(*) FROM orders WHERE status = 'pending_payment';

-- ===== payment_db =====
-- Blocking: every refund must reference an existing intent (currency is
-- backfilled from it).
SELECT 'refunds_without_intent' AS check_name, count(*) AS violations
  FROM payment_refunds r LEFT JOIN payment_intents p ON p.id = r.payment_intent_id WHERE p.id IS NULL;

-- Informational:
--  * orders_captured_twice: refunds for these orders must name the payment.
--  * legacy_refund_rows: rows written before Order refunds existed; they are
--    never synced to Order.
--  * captures_awaiting_review: outcomes Order refused; after the upgrade they
--    appear in Order's payment exceptions for a refund.
SELECT 'orders_captured_twice' AS check_name, count(*) AS rows FROM
  (SELECT order_id FROM payment_intents WHERE status IN ('captured', 'refunded') GROUP BY order_id HAVING count(*) > 1) d
UNION ALL SELECT 'legacy_refund_rows', count(*) FROM payment_refunds
UNION ALL SELECT 'captures_awaiting_review', count(*) FROM payment_order_sync WHERE requires_review;
