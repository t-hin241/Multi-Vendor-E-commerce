-- Refuses while receipt evidence exists: dropping the link would orphan it.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM case_attachments WHERE receipt_ref IS NOT NULL) THEN
        RAISE EXCEPTION 'receipt evidence exists; keep migration 000028';
    END IF;
END $$;
DROP INDEX case_attachments_receipt_expiry_idx;
DROP INDEX case_attachments_receipt_idx;
ALTER TABLE case_attachments DROP CONSTRAINT case_attachments_receipt_check;
ALTER TABLE case_attachments DROP CONSTRAINT case_attachments_attached_check;
ALTER TABLE case_attachments ADD CONSTRAINT case_attachments_check
    CHECK ((state = 'attached') = (message_id IS NOT NULL) OR state = 'deleted');
ALTER TABLE case_attachments DROP COLUMN receipt_ref, DROP COLUMN receipt_type;
