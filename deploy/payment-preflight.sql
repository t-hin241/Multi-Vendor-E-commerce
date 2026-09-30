-- Payment upgrade preflight (PAY-01..06). Read only; run against payment_db
-- before migrations 000007_receipts_operations and 000008_settlement_ledger.

-- Blocking: must be 0. Migration 000007 refuses to run otherwise.
--  * orders_with_several_pending_intents: decide which link stays open with
--    the provider (query it, cancel the others there), then set the others
--    to 'failed' with a reason under a reviewed change. Never delete them.
SELECT 'orders_with_several_pending_intents' AS check_name, count(*) AS violations FROM
  (SELECT order_id FROM payment_intents WHERE status = 'pending' GROUP BY order_id HAVING count(*) > 1) d
UNION ALL SELECT 'refunds_without_intent', count(*)
  FROM payment_refunds r LEFT JOIN payment_intents p ON p.id = r.payment_intent_id WHERE p.id IS NULL;

-- Informational: reviewed, not rewritten.
--  * refunded_intents_without_refund: 'refunded' with no succeeded refund
--    record. Reconcile with the provider; no receipt is fabricated.
--  * pending_intents_expired: closed by the reconciler after deploy. Intents
--    of the previous version have no stored provider reference and are
--    closed without a provider check; a late payment is still recorded.
--  * legacy_payout_items: per-vendor-order items of the old schema; the new
--    ledger does not include them. Succeeded ones without a reference block
--    nothing but should get their bank reference recorded.
--  * outcomes_awaiting_review: outcomes Order refused; retry from the admin
--    reconciliation screen after review.
SELECT 'refunded_intents_without_refund' AS check_name, count(*) AS rows FROM payment_intents p
  WHERE p.status = 'refunded' AND NOT EXISTS (SELECT 1 FROM payment_refunds r WHERE r.payment_intent_id = p.id AND r.status = 'succeeded')
UNION ALL SELECT 'pending_intents_expired', count(*) FROM payment_intents WHERE status = 'pending' AND expires_at < now()
UNION ALL SELECT 'pending_intents_without_expiry', count(*) FROM payment_intents WHERE status = 'pending' AND expires_at IS NULL
UNION ALL SELECT 'orders_captured_twice', count(*) FROM
  (SELECT order_id FROM payment_intents WHERE status IN ('captured', 'refunded') GROUP BY order_id HAVING count(*) > 1) d
UNION ALL SELECT 'legacy_payout_items', count(*) FROM payout_items
UNION ALL SELECT 'legacy_payout_items_succeeded_without_reference', count(*) FROM payout_items
  WHERE status = 'succeeded' AND provider_payout_id IS NULL
UNION ALL SELECT 'outcomes_awaiting_review', count(*) FROM payment_order_sync WHERE requires_review AND delivered_at IS NULL;
