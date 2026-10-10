-- PW-027 (AF-19): a restock request at or above
-- INVENTORY_RESTOCK_SECOND_APPROVAL_QUANTITY needs two different admins.
-- The first approval is recorded here; the stock moves only on the second.
ALTER TABLE restock_requests
    ADD COLUMN first_approved_by UUID,
    ADD COLUMN first_approved_at TIMESTAMPTZ;
ALTER TABLE restock_requests ADD CONSTRAINT restock_requests_two_person_check
    CHECK (first_approved_by IS NULL OR status <> 'approved' OR decided_by IS DISTINCT FROM first_approved_by);
