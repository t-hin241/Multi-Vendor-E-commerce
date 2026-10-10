-- Refuses once an admin enrolled: their second factor would be lost.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM admin_totp WHERE confirmed_at IS NOT NULL) THEN
        RAISE EXCEPTION 'admins enrolled a second factor; keep migration 000007';
    END IF;
END $$;
DROP TABLE admin_totp_recovery_codes;
DROP TABLE admin_totp;
