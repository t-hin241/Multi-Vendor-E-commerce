-- Refuses once an address was verified or a link was issued: the proof
-- would be lost.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM users WHERE email_verified_at IS NOT NULL) OR EXISTS (SELECT 1 FROM email_verification_tokens) THEN
        RAISE EXCEPTION 'email verifications exist; keep migration 000006';
    END IF;
END $$;
ALTER TABLE password_reset_deliveries DROP CONSTRAINT password_reset_deliveries_kind_ref_check;
ALTER TABLE password_reset_deliveries ALTER COLUMN reset_id SET NOT NULL;
ALTER TABLE password_reset_deliveries DROP COLUMN verification_id, DROP COLUMN kind;
DROP TABLE email_verification_tokens;
ALTER TABLE users DROP COLUMN email_verified_at;
