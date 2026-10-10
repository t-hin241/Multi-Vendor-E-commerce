-- PW-038: evidence images of a goods receipt (a return's, or a failed
-- delivery's) and of a return parcel marked lost reuse the private support
-- uploads (AF-01): an upload is attached either to a case message or to one
-- receipt reference, never both.
ALTER TABLE case_attachments
    ADD COLUMN receipt_type TEXT CHECK (receipt_type IN ('return', 'delivery_exception')),
    ADD COLUMN receipt_ref UUID;
ALTER TABLE case_attachments DROP CONSTRAINT case_attachments_check;
ALTER TABLE case_attachments ADD CONSTRAINT case_attachments_attached_check
    CHECK ((state = 'attached') = (message_id IS NOT NULL OR receipt_ref IS NOT NULL) OR state = 'deleted');
ALTER TABLE case_attachments ADD CONSTRAINT case_attachments_receipt_check
    CHECK ((receipt_type IS NULL) = (receipt_ref IS NULL) AND (receipt_ref IS NULL OR message_id IS NULL));
CREATE INDEX case_attachments_receipt_idx ON case_attachments (receipt_type, receipt_ref) WHERE receipt_ref IS NOT NULL;
-- Receipt photos are kept at most 30 days after the step they prove.
CREATE INDEX case_attachments_receipt_expiry_idx ON case_attachments (attached_at) WHERE receipt_ref IS NOT NULL AND state = 'attached';
