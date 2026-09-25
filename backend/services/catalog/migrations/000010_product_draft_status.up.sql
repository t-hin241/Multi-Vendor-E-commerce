ALTER TABLE products DROP CONSTRAINT products_status_check;
ALTER TABLE products ADD CONSTRAINT products_status_check CHECK (status IN ('draft', 'pending_review', 'approved', 'rejected'));
ALTER TABLE products ALTER COLUMN status SET DEFAULT 'draft';
