DROP TRIGGER inventory_stock_cache_changed ON inventory_items;
DROP FUNCTION inventory_queue_stock_cache();
DROP TABLE inventory_stock_outbox;
DROP TABLE inventory_operation_audit;
DROP TABLE inventory_outbox;
ALTER TABLE inventory_items DROP CONSTRAINT inventory_total_quantity_safe;
DROP INDEX stock_movements_operation_unique;
ALTER TABLE stock_movements DROP COLUMN operation_key, DROP COLUMN reserved_change, DROP COLUMN actor_user_id;
ALTER TABLE stock_reservations DROP CONSTRAINT reservation_operation_fk, DROP CONSTRAINT reservation_operation_line_unique;
DROP TABLE reservation_operations;
-- Expired receipts remain in the ledger; do not rewrite terminal history on rollback.
