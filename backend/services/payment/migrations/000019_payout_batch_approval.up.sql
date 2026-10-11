-- PW-027: a payout batch is created by one admin and approved by another
-- before any transfer result can be recorded. A batch over the configured
-- limits (batch total, one item) or paying a destination for the first
-- time needs a second, distinct approver. A rejected or withdrawn batch
-- cancels its items, so their entries are unpaid again.
-- Batches created before this migration keep 'not_required'.
ALTER TABLE payout_batches
    ADD COLUMN total_amount BIGINT CHECK (total_amount IS NULL OR total_amount >= 0),
    ADD COLUMN approval_state TEXT NOT NULL DEFAULT 'not_required'
        CHECK (approval_state IN ('not_required', 'awaiting_approval', 'awaiting_second_approval', 'approved', 'rejected', 'withdrawn')),
    ADD COLUMN required_approvals SMALLINT NOT NULL DEFAULT 0 CHECK (required_approvals BETWEEN 0 AND 2),
    ADD COLUMN risk_reasons TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN first_approved_by UUID,
    ADD COLUMN first_approved_at TIMESTAMPTZ,
    ADD COLUMN second_approved_by UUID,
    ADD COLUMN second_approved_at TIMESTAMPTZ,
    ADD COLUMN decided_by UUID,
    ADD COLUMN decision_reason TEXT CHECK (decision_reason IS NULL OR length(decision_reason) BETWEEN 1 AND 500),
    ADD COLUMN decided_at TIMESTAMPTZ,
    ADD CONSTRAINT payout_batches_independent_approvers CHECK (
        (first_approved_by IS NULL OR first_approved_by <> created_by)
        AND (second_approved_by IS NULL OR (first_approved_by IS NOT NULL AND second_approved_by <> created_by AND second_approved_by <> first_approved_by))),
    ADD CONSTRAINT payout_batches_approval_complete CHECK (
        approval_state <> 'approved'
        OR (required_approvals >= 1 AND first_approved_by IS NOT NULL AND (required_approvals = 1 OR second_approved_by IS NOT NULL)));
CREATE INDEX payout_batches_awaiting_idx ON payout_batches (created_at)
    WHERE approval_state IN ('awaiting_approval', 'awaiting_second_approval');

-- No transfer result before the batch is approved (cancelling stays open:
-- no money moves).
CREATE FUNCTION payout_item_requires_approved_batch() RETURNS trigger AS $$
BEGIN
    IF NEW.status IN ('succeeded', 'failed') AND NEW.status IS DISTINCT FROM OLD.status
       AND EXISTS (SELECT 1 FROM payout_batches b WHERE b.id = NEW.payout_batch_id
                   AND b.approval_state NOT IN ('not_required', 'approved')) THEN
        RAISE EXCEPTION 'payout batch % is not approved', NEW.payout_batch_id USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER payout_item_requires_approved_batch BEFORE UPDATE OF status ON payout_items
    FOR EACH ROW EXECUTE FUNCTION payout_item_requires_approved_batch();
