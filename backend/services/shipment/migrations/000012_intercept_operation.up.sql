-- PW-036: an interception Order asks for to cancel a package already
-- handed over records the request it serves; when the carrier accepts,
-- Shipment tells Order the goods are coming back (AF-04 exception
-- "returned"), and Order resolves the money and the goods there.
ALTER TABLE shipments ADD COLUMN intercept_operation TEXT
    CHECK (intercept_operation IS NULL OR length(intercept_operation) BETWEEN 1 AND 100);
