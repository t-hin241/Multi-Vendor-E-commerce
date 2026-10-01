-- REV-02..05: purchase proof, public author label, image upload intents and
-- a general moderation audit.

-- REV-05: "verified purchase" is set only by Review itself after Order
-- confirmed a completed purchase. Every earlier row came from the local
-- seed import (creating a review through the API never matched an eligible
-- item before this release), so none is marked verified.
ALTER TABLE reviews ADD COLUMN verified_purchase BOOLEAN NOT NULL DEFAULT false;

-- REV-03: the public author label is a masked snapshot ("N***A") taken when
-- the review is written; the public list never calls Identity. Older rows
-- are filled by a resumable backfill (author_label_checked_at).
ALTER TABLE reviews
    ADD COLUMN author_label TEXT CHECK (author_label IS NULL OR length(author_label) <= 40),
    ADD COLUMN author_label_checked_at TIMESTAMPTZ,
    ADD COLUMN hidden_note TEXT CHECK (hidden_note IS NULL OR length(hidden_note) <= 1000);
CREATE INDEX reviews_label_pending_idx ON reviews (created_at) WHERE author_label_checked_at IS NULL;
DROP INDEX IF EXISTS reviews_product_published_idx;
CREATE INDEX reviews_product_published_idx ON reviews (product_id, created_at DESC, id) WHERE status = 'published';
CREATE INDEX reviews_hidden_idx ON reviews (hidden_at DESC) WHERE status = 'hidden';

ALTER TABLE review_reports ADD CONSTRAINT review_reports_note_length CHECK (note IS NULL OR length(note) <= 1000) NOT VALID;
ALTER TABLE review_reports ADD CONSTRAINT review_reports_resolution_note_length
    CHECK (resolution_note IS NULL OR length(resolution_note) <= 1000) NOT VALID;
ALTER TABLE moderation_reasons ADD CONSTRAINT moderation_reasons_code_format CHECK (code ~ '^[a-z0-9_]{2,40}$') NOT VALID;
ALTER TABLE moderation_reasons ADD CONSTRAINT moderation_reasons_label_length CHECK (length(label) BETWEEN 1 AND 100) NOT VALID;

-- REV-04: an upload is recorded before the object is stored and removed in
-- the transaction that records the image. An intent left behind (failed
-- metadata write, stopped process) is cleaned up with retry, so a failed
-- upload never leaves an object behind for good.
CREATE TABLE review_image_uploads (
    object_key TEXT PRIMARY KEY,
    review_id UUID NOT NULL REFERENCES reviews (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '15 minutes',
    last_error TEXT CHECK (last_error IS NULL OR length(last_error) <= 300),
    parked_at TIMESTAMPTZ
);
CREATE INDEX review_image_uploads_due_idx ON review_image_uploads (next_attempt_at) WHERE parked_at IS NULL;
CREATE INDEX review_image_uploads_review_idx ON review_image_uploads (review_id);

-- REV-04/ADM-01: every moderation and shop action is audited (reply,
-- report, decision, hide/restore, reason changes), not only decisions.
ALTER TABLE review_moderation_audit_logs DISABLE TRIGGER review_audit_append_only;
ALTER TABLE review_moderation_audit_logs
    ALTER COLUMN review_id DROP NOT NULL,
    ADD COLUMN entity_type TEXT NOT NULL DEFAULT 'review' CHECK (entity_type IN ('review', 'moderation_reason')),
    ADD COLUMN entity_id TEXT,
    ADD COLUMN changes JSONB;
UPDATE review_moderation_audit_logs SET entity_id = review_id::text;
ALTER TABLE review_moderation_audit_logs ENABLE TRIGGER review_audit_append_only;
-- A writer that does not know entity_id (an image rolled back to the
-- previous version) still records a complete row.
CREATE FUNCTION review_audit_fill_entity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.entity_id IS NULL THEN
  NEW.entity_id := NEW.review_id::text;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER review_audit_fill_entity BEFORE INSERT ON review_moderation_audit_logs
FOR EACH ROW EXECUTE FUNCTION review_audit_fill_entity();
ALTER TABLE review_moderation_audit_logs ALTER COLUMN entity_id SET NOT NULL;
ALTER TABLE review_moderation_audit_logs ADD CONSTRAINT review_audit_note_length CHECK (note IS NULL OR length(note) <= 1000) NOT VALID;
CREATE INDEX review_audit_entity_idx ON review_moderation_audit_logs (entity_id);
