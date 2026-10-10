-- PW-028: an admin's authenticator app (TOTP, RFC 6238) is the second
-- factor of reauthentication for money operations. The secret is stored
-- encrypted (IDENTITY_MFA_ENCRYPTION_KEY); recovery codes only as hashes.
CREATE TABLE admin_totp (
    user_id UUID PRIMARY KEY REFERENCES users (id),
    secret_ciphertext BYTEA NOT NULL,
    confirmed_at TIMESTAMPTZ,
    -- The newest step a code was accepted for: a code works once.
    last_used_step BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE admin_totp_recovery_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users (id),
    code_hash TEXT NOT NULL UNIQUE,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX admin_totp_recovery_codes_user_idx ON admin_totp_recovery_codes (user_id) WHERE used_at IS NULL;
