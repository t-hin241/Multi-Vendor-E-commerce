ALTER TABLE users
    ADD COLUMN phone TEXT NOT NULL DEFAULT '',
    ADD COLUMN profile_version BIGINT NOT NULL DEFAULT 1 CHECK (profile_version > 0),
    ADD COLUMN auth_version BIGINT NOT NULL DEFAULT 1 CHECK (auth_version > 0);

CREATE TABLE email_change_operations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    token_hash TEXT NOT NULL UNIQUE,
    target_ciphertext BYTEA,
    auth_version BIGINT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX email_change_active_user ON email_change_operations(user_id) WHERE used_at IS NULL;
CREATE INDEX email_change_created ON email_change_operations(user_id, created_at);

ALTER TABLE password_reset_deliveries
    DROP CONSTRAINT password_reset_deliveries_kind_check,
    DROP CONSTRAINT password_reset_deliveries_kind_ref_check,
    ADD COLUMN email_change_id UUID REFERENCES email_change_operations(id),
    ADD CONSTRAINT password_reset_deliveries_kind_check CHECK (kind IN ('password_reset', 'email_verification', 'email_change', 'email_changed')),
    ADD CONSTRAINT password_reset_deliveries_kind_ref_check CHECK (
        (kind = 'password_reset' AND reset_id IS NOT NULL AND verification_id IS NULL AND email_change_id IS NULL) OR
        (kind = 'email_verification' AND verification_id IS NOT NULL AND reset_id IS NULL AND email_change_id IS NULL) OR
        (kind IN ('email_change', 'email_changed') AND email_change_id IS NOT NULL AND reset_id IS NULL AND verification_id IS NULL)
    );
