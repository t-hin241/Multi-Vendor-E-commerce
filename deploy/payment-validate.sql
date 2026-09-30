-- Payment upgrade validation. Read only; run against payment_db after
-- migrations 000007 and 000008. Every count must be 0.
SELECT 'events_without_receipt' AS check_name, count(*) AS violations
  FROM payment_events e LEFT JOIN payment_receipts r ON r.provider_event_id = e.provider_event_id WHERE r.id IS NULL
UNION ALL SELECT 'orders_with_several_open_intents', count(*) FROM
  (SELECT order_id FROM payment_intents WHERE status IN ('creating', 'pending') GROUP BY order_id HAVING count(*) > 1) d
UNION ALL SELECT 'refunds_over_capture', count(*) FROM
  (SELECT p.id FROM payment_intents p JOIN payment_refunds r ON r.payment_intent_id = p.id AND r.status <> 'failed'
   GROUP BY p.id, p.amount HAVING sum(r.amount) > p.amount) d
UNION ALL SELECT 'entries_paid_twice', count(*) FROM
  (SELECT l.entry_id FROM payout_item_entries l JOIN payout_items i ON i.id = l.payout_item_id
   WHERE i.status IN ('pending', 'succeeded') GROUP BY l.entry_id HAVING count(*) > 1) d
UNION ALL SELECT 'payout_amount_mismatch', count(*) FROM
  (SELECT i.id FROM payout_items i JOIN payout_item_entries l ON l.payout_item_id = i.id JOIN settlement_entries e ON e.id = l.entry_id
   GROUP BY i.id, i.amount HAVING sum(e.amount) <> i.amount) d;
