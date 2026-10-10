-- PW-022: an account proves it owns its email address with a one-time
-- link. The link travels like a password reset link: the token is stored
-- hashed, its encrypted copy waits in the delivery queue (now shared by
-- both kinds) until Notification fetched it once.
ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMPTZ;

CREATE TABLE email_verification_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users (id),
    -- The address verified: changing the account's email voids the link.
    email TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX email_verification_tokens_user_idx ON email_verification_tokens (user_id, created_at DESC);

ALTER TABLE password_reset_deliveries
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'password_reset' CHECK (kind IN ('password_reset', 'email_verification')),
    ADD COLUMN verification_id UUID REFERENCES email_verification_tokens (id),
    ALTER COLUMN reset_id DROP NOT NULL;
ALTER TABLE password_reset_deliveries ADD CONSTRAINT password_reset_deliveries_kind_ref_check
    CHECK ((kind = 'password_reset' AND reset_id IS NOT NULL AND verification_id IS NULL)
        OR (kind = 'email_verification' AND verification_id IS NOT NULL AND reset_id IS NULL));
