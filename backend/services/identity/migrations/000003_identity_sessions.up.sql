-- Add session families, reset delivery storage and identity audit records.
ALTER TABLE refresh_tokens ADD COLUMN family_id TEXT;
-- Enforce email uniqueness after trimming whitespace and converting to lowercase.
CREATE UNIQUE INDEX users_normalized_email_key ON users(lower(btrim(email)));
CREATE INDEX refresh_tokens_family_idx ON refresh_tokens(family_id) WHERE revoked_at IS NULL;
CREATE INDEX password_reset_expiry_idx ON password_reset_tokens(expires_at) WHERE used_at IS NULL;

CREATE TABLE password_reset_deliveries (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 reset_id UUID NOT NULL REFERENCES password_reset_tokens(id),
 user_id UUID NOT NULL REFERENCES users(id),
 encrypted_token BYTEA,
 expires_at TIMESTAMPTZ NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','sending','sent','failed','expired')),
 attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 lease_until TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX password_reset_delivery_due_idx ON password_reset_deliveries(next_attempt_at) WHERE status IN ('pending','sending');
CREATE TABLE identity_audit_logs (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 actor_id UUID REFERENCES users(id),
 user_id UUID NOT NULL REFERENCES users(id),
 action TEXT NOT NULL,
 reason TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX identity_audit_user_idx ON identity_audit_logs(user_id, created_at DESC);
