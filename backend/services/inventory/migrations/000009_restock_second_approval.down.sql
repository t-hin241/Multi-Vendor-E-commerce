-- Refuses while a first approval is recorded: it would be lost.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM restock_requests WHERE first_approved_by IS NOT NULL) THEN
        RAISE EXCEPTION 'restock requests carry a first approval; keep migration 000009';
    END IF;
END $$;
ALTER TABLE restock_requests DROP CONSTRAINT restock_requests_two_person_check;
ALTER TABLE restock_requests DROP COLUMN first_approved_at, DROP COLUMN first_approved_by;
