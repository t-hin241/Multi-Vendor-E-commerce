DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM statement_exports) THEN
        RAISE EXCEPTION 'retain statement operations; disable the feature instead';
    END IF;
END $$;
DROP TABLE statement_export_entries;
DROP TABLE statement_exports;
DROP FUNCTION statement_export_snapshot_immutable();
DROP INDEX settlement_entries_source_idx;
DROP INDEX settlement_entries_statement_idx;
ALTER TABLE settlement_entries DROP COLUMN occurred_at;
