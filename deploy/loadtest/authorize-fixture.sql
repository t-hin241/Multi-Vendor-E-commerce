-- Synthetic, non-login identities. ONLY the isolated shopee-loadtest copy.
-- The driver executes this as a fixture administrator, never an app role.
\set ON_ERROR_STOP on
\connect identity_db
INSERT INTO users (id,email,password_hash,full_name,role)
SELECT format('02000000-0000-4000-8000-%s', lpad(n::text,12,'0'))::uuid,
       format('pw020-%s@example.invalid',n), 'synthetic-non-login-fixture', 'PW-020 synthetic actor',
       CASE WHEN n <= 32 THEN 'vendor' ELSE 'buyer' END
FROM generate_series(1,64) n ON CONFLICT DO NOTHING;
\connect vendor_db
INSERT INTO vendors (id,user_id,shop_name,status)
SELECT format('02000001-0000-4000-8000-%s',lpad(n::text,12,'0'))::uuid,
       format('02000000-0000-4000-8000-%s',lpad(n::text,12,'0'))::uuid,
       format('PW-020 synthetic shop %s',n), 'approved'
FROM generate_series(1,32) n ON CONFLICT DO NOTHING;
INSERT INTO vendor_memberships (vendor_id,user_id,role,status)
SELECT format('02000001-0000-4000-8000-%s',lpad(n::text,12,'0'))::uuid,
       format('02000000-0000-4000-8000-%s',lpad((n+32)::text,12,'0'))::uuid,'staff','active'
FROM generate_series(1,32) n ON CONFLICT DO NOTHING;
INSERT INTO membership_permissions (vendor_id,user_id,permission)
SELECT vendor_id,user_id,'orders.read' FROM vendor_memberships
WHERE vendor_id::text LIKE '02000001-0000-4000-8000-%' AND role='staff'
ON CONFLICT DO NOTHING;
