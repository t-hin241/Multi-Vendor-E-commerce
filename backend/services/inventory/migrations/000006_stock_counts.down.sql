-- The matching stock_movements rows ('stock_count') stay in the ledger; a
-- rollback never rewrites stock history.
DROP TABLE IF EXISTS inventory_stock_counts;
