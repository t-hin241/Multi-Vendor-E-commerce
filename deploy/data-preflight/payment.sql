-- Read-only data preflight: payment_db (data tooling plan 14, DEV-04).
SELECT 'blocking'::text AS severity, 'migration_ledger_dirty'::text AS check_name, count(*)::bigint AS row_count,
       'a migration stopped half-way; resolve before anything else'::text AS meaning
  FROM schema_migrations WHERE dirty
UNION ALL
SELECT 'blocking', 'duplicate_provider_event', coalesce(sum(n - 1), 0),
       'provider webhook events recorded more than once'
  FROM (SELECT count(*) AS n FROM payment_events GROUP BY provider_event_id HAVING count(*) > 1) d
UNION ALL
SELECT 'blocking', 'non_positive_amount', count(*),
       'payment intents, refunds or payouts of zero or less'
  FROM (SELECT amount FROM payment_intents UNION ALL SELECT amount FROM payment_refunds UNION ALL SELECT amount FROM payout_items) a
 WHERE amount <= 0
UNION ALL
SELECT 'blocking', 'refunds_exceed_payment', count(*),
       'payments whose succeeded and open refunds exceed the amount paid'
  FROM payment_intents pi
 WHERE (SELECT coalesce(sum(r.amount), 0) FROM payment_refunds r
         WHERE r.payment_intent_id = pi.id AND r.status IN ('succeeded', 'pending', 'awaiting_provider_refund')) > pi.amount
UNION ALL
SELECT 'blocking', 'refund_of_uncaptured_payment', count(*),
       'succeeded refunds of a payment that was never captured'
  FROM payment_refunds r JOIN payment_intents pi ON pi.id = r.payment_intent_id
 WHERE r.status = 'succeeded' AND pi.status NOT IN ('captured', 'refunded')
UNION ALL
SELECT 'blocking', 'payout_without_settlement', count(*),
       'payouts for a vendor order that never became settleable'
  FROM payout_items p
 WHERE p.vendor_order_id IS NOT NULL AND p.status <> 'failed'
   AND NOT EXISTS (SELECT 1 FROM settlement_vendor_orders s WHERE s.vendor_order_id = p.vendor_order_id)
UNION ALL
SELECT 'blocking', 'paid_out_twice', count(*),
       'vendor orders with more than one payout that is not failed'
  FROM (SELECT vendor_order_id FROM payout_items WHERE vendor_order_id IS NOT NULL AND status <> 'failed'
         GROUP BY vendor_order_id HAVING count(*) > 1) d
UNION ALL
SELECT 'warning', 'events_parked', count(*),
       'events Payment could not apply (see /admin/events)'
  FROM event_inbox WHERE status = 'parked';
