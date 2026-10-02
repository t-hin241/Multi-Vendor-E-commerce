-- Read-only data preflight: order_db (data tooling plan 14, DEV-04).
-- Money rules (order domain, checkout.go/order.go): item subtotal = price x
-- quantity; vendor order subtotal = its items; order subtotal = its vendor
-- orders; order shipping = vendor order fees; total = subtotal + shipping;
-- commission + net = commission base.
SELECT 'blocking'::text AS severity, 'migration_ledger_dirty'::text AS check_name, count(*)::bigint AS row_count,
       'a migration stopped half-way; resolve before anything else'::text AS meaning
  FROM schema_migrations WHERE dirty
UNION ALL
SELECT 'blocking', 'item_subtotal_mismatch', count(*),
       'order items whose subtotal is not price x quantity'
  FROM order_items WHERE subtotal_amount <> price_amount * quantity
UNION ALL
SELECT 'blocking', 'vendor_order_subtotal_mismatch', count(*),
       'vendor orders whose subtotal is not the sum of their items'
  FROM vendor_orders vo
 WHERE vo.subtotal_amount <> coalesce((SELECT sum(i.subtotal_amount) FROM order_items i WHERE i.vendor_order_id = vo.id), 0)
UNION ALL
SELECT 'blocking', 'order_subtotal_mismatch', count(*),
       'orders whose subtotal is not the sum of their vendor orders'
  FROM orders o
 WHERE o.subtotal_amount IS NOT NULL
   AND o.subtotal_amount <> coalesce((SELECT sum(vo.subtotal_amount) FROM vendor_orders vo WHERE vo.order_id = o.id), 0)
UNION ALL
SELECT 'blocking', 'order_shipping_mismatch', count(*),
       'orders whose shipping is not the sum of their vendor order fees'
  FROM orders o
 WHERE o.shipping_amount IS NOT NULL
   AND o.shipping_amount <> coalesce((SELECT sum(vo.shipping_fee_amount) FROM vendor_orders vo WHERE vo.order_id = o.id), 0)
UNION ALL
SELECT 'blocking', 'order_total_mismatch', count(*),
       'orders whose total is not subtotal + shipping'
  FROM orders WHERE subtotal_amount IS NOT NULL AND total_amount <> subtotal_amount + coalesce(shipping_amount, 0)
UNION ALL
SELECT 'blocking', 'commission_mismatch', count(*),
       'vendor orders whose commission + net differ from the commission base'
  FROM vendor_orders
 WHERE commission_amount IS NOT NULL AND net_amount IS NOT NULL AND commission_base_amount IS NOT NULL
   AND commission_amount + net_amount <> commission_base_amount
UNION ALL
SELECT 'blocking', 'refund_exceeds_order', count(*),
       'orders or vendor orders refunded beyond what was charged'
  FROM (SELECT id FROM orders WHERE refunded_amount > total_amount
        UNION ALL
        SELECT id FROM vendor_orders WHERE refunded_amount > subtotal_amount + coalesce(shipping_fee_amount, 0)) r
UNION ALL
SELECT 'warning', 'refunded_amount_differs_from_refunds', count(*),
       'vendor orders whose refunded amount differs from their succeeded refunds'
  FROM vendor_orders vo
 WHERE coalesce(vo.refunded_amount, 0) <> coalesce((SELECT sum(r.amount) FROM order_refunds r
                                                     WHERE r.vendor_order_id = vo.id AND r.status = 'succeeded'), 0)
UNION ALL
SELECT 'blocking', 'paid_without_applied_payment', count(*),
       'orders past payment with no applied payment recorded'
  FROM orders o
 WHERE (o.paid_at IS NOT NULL OR o.status IN ('paid', 'processing', 'shipped', 'completed'))
   AND NOT EXISTS (SELECT 1 FROM order_payments p WHERE p.order_id = o.id AND p.outcome = 'applied')
UNION ALL
SELECT 'blocking', 'applied_payment_amount_mismatch', count(*),
       'applied payments whose amount or currency differ from the order'
  FROM order_payments p JOIN orders o ON o.id = p.order_id
 WHERE p.outcome = 'applied' AND (p.amount <> o.total_amount OR p.currency <> o.currency)
UNION ALL
SELECT 'blocking', 'fulfilled_before_payment', count(*),
       'vendor orders shipped or completed while the order is unpaid or cancelled'
  FROM vendor_orders vo JOIN orders o ON o.id = vo.order_id
 WHERE vo.status IN ('shipped', 'completed') AND o.status IN ('pending_payment', 'cancelled')
UNION ALL
SELECT 'warning', 'events_parked', count(*),
       'events Order could not apply (see /admin/events)'
  FROM event_inbox WHERE status = 'parked';
