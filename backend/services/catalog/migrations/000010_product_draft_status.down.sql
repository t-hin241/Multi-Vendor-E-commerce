ALTER TABLE products ALTER COLUMN status SET DEFAULT 'pending_review';
ALTER TABLE products DROP CONSTRAINT products_status_check;
ALTER TABLE products ADD CONSTRAINT products_status_check CHECK (status IN ('pending_review', 'approved', 'rejected'));
