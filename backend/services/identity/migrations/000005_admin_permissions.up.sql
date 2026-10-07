-- AF-19: scoped admin permissions and recent-reauthentication proofs.
-- No admin receives a bundle here: grants are provisioned explicitly
-- (bootstrap-access, then access.manage) before
-- FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED turns deny-by-default on.

-- Bumped on every grant change of the admin; services record it next to
-- the mutation it allowed.
ALTER TABLE users ADD COLUMN permission_version BIGINT NOT NULL DEFAULT 0 CHECK (permission_version >= 0);

CREATE TABLE admin_permission_grants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users (id),
    bundle TEXT NOT NULL CHECK (bundle IN ('support.manage', 'moderation.manage', 'finance.read', 'finance.prepare',
        'finance.approve', 'platform.configure', 'analytics.read', 'audit.read', 'access.manage')),
    status TEXT NOT NULL CHECK (status IN ('active', 'revoked')),
    -- NULL only for the audited bootstrap command.
    granted_by UUID REFERENCES users (id),
    reason TEXT NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_by UUID REFERENCES users (id),
    revoke_reason TEXT CHECK (revoke_reason IS NULL OR length(revoke_reason) <= 500),
    revoked_at TIMESTAMPTZ,
    CHECK ((status = 'revoked') = (revoked_at IS NOT NULL)),
    CHECK (granted_by IS NULL OR granted_by <> user_id)
);
CREATE UNIQUE INDEX admin_permission_grants_active_idx ON admin_permission_grants (user_id, bundle) WHERE status = 'active';
CREATE INDEX admin_permission_grants_user_idx ON admin_permission_grants (user_id, created_at DESC);

-- One-time proofs: only the hash is stored; a proof is bound to the user,
-- a purpose and an operation, and lives five minutes.
CREATE TABLE reauth_proofs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    proof_hash BYTEA NOT NULL UNIQUE,
    user_id UUID NOT NULL REFERENCES users (id),
    purpose TEXT NOT NULL CHECK (purpose ~ '^[a-z][a-z0-9_.:-]{2,63}$'),
    operation_hash TEXT NOT NULL CHECK (length(operation_hash) BETWEEN 1 AND 200),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX reauth_proofs_expiry_idx ON reauth_proofs (expires_at);
