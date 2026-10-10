-- Refuses once a reimbursement exists: the book would be lost.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM reimbursements) THEN
        RAISE EXCEPTION 'reimbursements exist; keep migration 000018';
    END IF;
END $$;
DROP TABLE reimbursements;
DROP FUNCTION reimbursements_guard();
