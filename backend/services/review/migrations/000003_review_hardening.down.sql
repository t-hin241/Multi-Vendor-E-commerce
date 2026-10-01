-- Refuses while rolling back would lose purchase proof, audit or an
-- object that still needs cleaning up.
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM reviews WHERE verified_purchase) THEN
  RAISE EXCEPTION 'reviews are marked as verified purchases; rolling back would lose that proof';
 END IF;
 IF EXISTS (SELECT 1 FROM review_moderation_audit_logs WHERE entity_type <> 'review' OR review_id IS NULL OR changes IS NOT NULL) THEN
  RAISE EXCEPTION 'audit rows of this version exist; audit is never deleted';
 END IF;
 IF EXISTS (SELECT 1 FROM review_image_uploads) THEN
  RAISE EXCEPTION 'image uploads still need cleaning up';
 END IF;
END $$;

DROP INDEX IF EXISTS review_audit_entity_idx;
ALTER TABLE review_moderation_audit_logs DROP CONSTRAINT IF EXISTS review_audit_note_length;
DROP TRIGGER IF EXISTS review_audit_fill_entity ON review_moderation_audit_logs;
DROP FUNCTION IF EXISTS review_audit_fill_entity();
ALTER TABLE review_moderation_audit_logs DROP COLUMN changes, DROP COLUMN entity_id, DROP COLUMN entity_type;
ALTER TABLE review_moderation_audit_logs ALTER COLUMN review_id SET NOT NULL;

DROP TABLE IF EXISTS review_image_uploads;

ALTER TABLE moderation_reasons DROP CONSTRAINT IF EXISTS moderation_reasons_label_length;
ALTER TABLE moderation_reasons DROP CONSTRAINT IF EXISTS moderation_reasons_code_format;
ALTER TABLE review_reports DROP CONSTRAINT IF EXISTS review_reports_resolution_note_length;
ALTER TABLE review_reports DROP CONSTRAINT IF EXISTS review_reports_note_length;

DROP INDEX IF EXISTS reviews_hidden_idx;
DROP INDEX IF EXISTS reviews_product_published_idx;
CREATE INDEX reviews_product_published_idx ON reviews (product_id, created_at DESC) WHERE status = 'published';
DROP INDEX IF EXISTS reviews_label_pending_idx;
ALTER TABLE reviews DROP COLUMN hidden_note, DROP COLUMN author_label_checked_at, DROP COLUMN author_label, DROP COLUMN verified_purchase;
