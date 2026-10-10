-- Refuses once evidence was attached to a report: it would be lost.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM shipment_evidence WHERE state = 'attached') THEN
        RAISE EXCEPTION 'failure reports carry evidence; keep migration 000013';
    END IF;
END $$;
DROP TABLE shipment_evidence;
