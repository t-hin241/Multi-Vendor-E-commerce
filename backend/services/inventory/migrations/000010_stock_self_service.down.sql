DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM stock_adjustment_operations) OR EXISTS(SELECT 1 FROM inventory_self_service_receipts)
 OR EXISTS(SELECT 1 FROM stock_alert_episode) THEN
  RAISE EXCEPTION 'AF-13 history exists; disable the feature instead of dropping audit history';
 END IF;
END $$;
DROP TRIGGER inventory_self_service_changed ON inventory_items;
DROP FUNCTION inventory_self_service_version();
DROP TABLE stock_alert_episode,inventory_self_service_receipts,stock_adjustment_operations,inventory_vendor_policy;
DROP INDEX stock_movements_history_idx;
DROP TRIGGER stock_movements_append_only ON stock_movements;
ALTER TABLE stock_movements DROP COLUMN source,DROP COLUMN adjustment_reason,DROP COLUMN evidence_ref;
ALTER TABLE inventory_items DROP COLUMN version,DROP COLUMN low_stock_threshold,DROP COLUMN alert_enabled,DROP COLUMN alert_active,DROP COLUMN alert_episode;
