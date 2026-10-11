-- Refuses once a batch went through approval: dropping the record of who
-- approved it would lose the two-person trail.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM payout_batches WHERE approval_state <> 'not_required') THEN
        RAISE EXCEPTION 'payout batches carry approvals; keep migration 000019';
    END IF;
END $$;
DROP TRIGGER payout_item_requires_approved_batch ON payout_items;
DROP FUNCTION payout_item_requires_approved_batch();
DROP INDEX payout_batches_awaiting_idx;
ALTER TABLE payout_batches DROP CONSTRAINT payout_batches_approval_complete,
    DROP CONSTRAINT payout_batches_independent_approvers,
    DROP COLUMN decided_at, DROP COLUMN decision_reason, DROP COLUMN decided_by,
    DROP COLUMN second_approved_at, DROP COLUMN second_approved_by,
    DROP COLUMN first_approved_at, DROP COLUMN first_approved_by,
    DROP COLUMN risk_reasons, DROP COLUMN required_approvals, DROP COLUMN approval_state, DROP COLUMN total_amount;
