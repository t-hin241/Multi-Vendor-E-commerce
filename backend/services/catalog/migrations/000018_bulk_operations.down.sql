-- Imported products stay (they are ordinary products); only an unused
-- feature can be removed. After real use, disable the flag instead.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM import_jobs WHERE confirmed_at IS NOT NULL) THEN
  RAISE EXCEPTION 'Bulk imports have been applied; disable FEATURE_BULK_OPERATIONS_ENABLED instead of rolling back';
 END IF;
END $$;
DROP TABLE product_import_refs;
DROP TABLE import_job_events;
DROP FUNCTION import_job_events_append_only();
DROP TABLE import_exports;
DROP TABLE import_rows;
DROP FUNCTION import_rows_immutable();
DROP FUNCTION import_rows_no_delete();
DROP TABLE import_jobs;
