-- Read only; duplicate lines and mixed terminal states must be reviewed before migration 5.
SELECT 'duplicate_operation_lines' AS check_name,count(*) AS violations FROM
 (SELECT order_id,inventory_item_id FROM stock_reservations GROUP BY order_id,inventory_item_id HAVING count(*)>1) d
UNION ALL SELECT 'mixed_operation_states',count(*) FROM
 (SELECT order_id FROM stock_reservations GROUP BY order_id HAVING count(DISTINCT status)>1) d
UNION ALL SELECT 'reserved_mismatches',count(*) FROM inventory_items i LEFT JOIN
 (SELECT inventory_item_id,sum(quantity)::numeric held FROM stock_reservations WHERE status='active' GROUP BY inventory_item_id)s ON s.inventory_item_id=i.id
 WHERE i.reserved_quantity::numeric<>coalesce(s.held,0)
UNION ALL SELECT 'quantity_overflow',count(*) FROM inventory_items WHERE available_quantity::numeric+reserved_quantity::numeric>9223372036854775807;

-- Informational: never release these in a bulk SQL migration.
SELECT status,count(*) AS lines,count(DISTINCT order_id) AS orders,
 count(*) FILTER(WHERE expires_at<=now()) AS past_deadline
FROM stock_reservations GROUP BY status ORDER BY status;
