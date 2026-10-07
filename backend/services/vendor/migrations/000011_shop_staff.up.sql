-- AF-17: shop staff. Vendor owns memberships, their permission grants and
-- invitations; Identity stays the source of truth for the account itself.

-- Preflight: every shop must have an owner to backfill. Never pick anyone
-- else as owner.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM vendors WHERE user_id IS NULL) THEN
  RAISE EXCEPTION 'shop staff preflight: a shop has no owner; fix it before applying 000011';
 END IF;
END $$;

-- One row per (shop, person). The owner row mirrors vendors.user_id (no
-- ownership transfer in v1) and is never revoked; owner permissions are
-- implicit, staff permissions are rows in membership_permissions.
CREATE TABLE vendor_memberships (
    vendor_id UUID NOT NULL REFERENCES vendors (id),
    user_id UUID NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('owner', 'staff')),
    status TEXT NOT NULL CHECK (status IN ('active', 'revoked')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    invitation_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ,
    PRIMARY KEY (vendor_id, user_id),
    CHECK (role = 'staff' OR status = 'active'),
    CHECK ((status = 'revoked') = (revoked_at IS NOT NULL))
);
CREATE UNIQUE INDEX vendor_memberships_owner_idx ON vendor_memberships (vendor_id) WHERE role = 'owner';
CREATE INDEX vendor_memberships_user_idx ON vendor_memberships (user_id, status, vendor_id);

-- scope_mode 'selected' (per-warehouse grants) arrives with multi-warehouse
-- (AF-32); until then every grant covers the whole shop.
CREATE TABLE membership_permissions (
    vendor_id UUID NOT NULL,
    user_id UUID NOT NULL,
    permission TEXT NOT NULL CHECK (permission ~ '^[a-z_]+(\.[a-z_]+)+$'),
    scope_mode TEXT NOT NULL DEFAULT 'all' CHECK (scope_mode IN ('all')),
    PRIMARY KEY (vendor_id, user_id, permission),
    FOREIGN KEY (vendor_id, user_id) REFERENCES vendor_memberships (vendor_id, user_id)
);

-- Invitations keep neither the raw token nor the raw address once sent:
-- token_hash is replaced on each delivery attempt, delivery_email is
-- cleared when the invitation is sent or closed, email_fingerprint (keyed
-- HMAC) is what acceptance compares.
CREATE TABLE staff_invitations (
    id UUID PRIMARY KEY,
    vendor_id UUID NOT NULL REFERENCES vendors (id),
    email_fingerprint TEXT NOT NULL CHECK (length(email_fingerprint) = 64),
    email_hint TEXT NOT NULL CHECK (length(email_hint) <= 120),
    delivery_email TEXT CHECK (delivery_email IS NULL OR length(delivery_email) <= 254),
    permissions TEXT[] NOT NULL CHECK (cardinality(permissions) BETWEEN 1 AND 32),
    status TEXT NOT NULL CHECK (status IN ('pending', 'accepted', 'revoked', 'superseded')),
    token_hash BYTEA,
    invited_by UUID NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_user_id UUID,
    accepted_at TIMESTAMPTZ,
    delivery_status TEXT NOT NULL DEFAULT 'queued' CHECK (delivery_status IN ('queued', 'sent', 'parked', 'closed')),
    delivery_attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_until TIMESTAMPTZ,
    last_error TEXT CHECK (last_error IS NULL OR length(last_error) <= 120),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((status = 'accepted') = (accepted_user_id IS NOT NULL AND accepted_at IS NOT NULL))
);
CREATE UNIQUE INDEX staff_invitations_token_idx ON staff_invitations (token_hash) WHERE token_hash IS NOT NULL;
CREATE UNIQUE INDEX staff_invitations_pending_idx ON staff_invitations (vendor_id, email_fingerprint) WHERE status = 'pending';
CREATE INDEX staff_invitations_vendor_idx ON staff_invitations (vendor_id, created_at DESC);
CREATE INDEX staff_invitations_delivery_idx ON staff_invitations (next_attempt_at) WHERE delivery_status = 'queued';

-- Append-only: permission names before/after, never a raw token or address.
CREATE TABLE membership_audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id UUID NOT NULL REFERENCES vendors (id),
    member_user_id UUID,
    invitation_id UUID,
    actor_user_id UUID NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('staff_invited', 'staff_invitation_revoked', 'staff_joined',
        'staff_permissions_changed', 'staff_revoked')),
    old_permissions TEXT[] NOT NULL DEFAULT '{}',
    new_permissions TEXT[] NOT NULL DEFAULT '{}',
    membership_version BIGINT,
    reason TEXT CHECK (reason IS NULL OR length(reason) <= 500),
    request_id TEXT CHECK (request_id IS NULL OR length(request_id) <= 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX membership_audit_logs_vendor_idx ON membership_audit_logs (vendor_id, created_at DESC);
CREATE INDEX membership_audit_logs_recent_idx ON membership_audit_logs (created_at DESC, id);
CREATE TRIGGER membership_audit_append_only BEFORE UPDATE OR DELETE ON membership_audit_logs
FOR EACH ROW EXECUTE FUNCTION vendor_audit_append_only();

-- Backfill owners, and keep every new shop's owner row in the same
-- transaction as the shop — also for an older Vendor image after rollback.
INSERT INTO vendor_memberships (vendor_id, user_id, role, status, created_at, updated_at)
SELECT id, user_id, 'owner', 'active', created_at, now() FROM vendors
ON CONFLICT DO NOTHING;

CREATE FUNCTION vendor_owner_membership() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO vendor_memberships (vendor_id, user_id, role, status) VALUES (NEW.id, NEW.user_id, 'owner', 'active')
    ON CONFLICT DO NOTHING;
    RETURN NEW;
END $$;
CREATE TRIGGER vendors_owner_membership AFTER INSERT ON vendors
FOR EACH ROW EXECUTE FUNCTION vendor_owner_membership();

-- No ownership transfer in v1: the owner of a shop never changes.
CREATE FUNCTION vendor_owner_fixed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.user_id IS DISTINCT FROM OLD.user_id THEN
        RAISE EXCEPTION 'shop ownership cannot change';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER vendors_owner_fixed BEFORE UPDATE OF user_id ON vendors
FOR EACH ROW EXECUTE FUNCTION vendor_owner_fixed();
