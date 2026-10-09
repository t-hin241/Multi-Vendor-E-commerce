-- AF-05: where a shop receives returned goods. The owner designates one of
-- the shop's addresses (pickup addresses are not return addresses by
-- default) with its receiving hours; an admin verifies that exact version.
-- Editing the address or the designation bumps the version, so Order only
-- ever snapshots a destination someone checked. Order keeps its own
-- snapshot on each return: a later change never moves a parcel in transit.

CREATE TABLE vendor_return_destinations (
    vendor_id UUID PRIMARY KEY REFERENCES vendors (id),
    address_id UUID NOT NULL REFERENCES vendor_addresses (id) ON DELETE RESTRICT,
    receiving_hours TEXT NOT NULL CHECK (length(receiving_hours) BETWEEN 1 AND 200),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    verified_version BIGINT CHECK (verified_version IS NULL OR verified_version <= version),
    verified_by UUID,
    verified_at TIMESTAMPTZ,
    rejection_reason TEXT CHECK (rejection_reason IS NULL OR length(rejection_reason) <= 500),
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((verified_version IS NULL) = (verified_at IS NULL))
);
CREATE INDEX vendor_return_destinations_address_idx ON vendor_return_destinations (address_id);

ALTER TABLE vendor_audit_logs DROP CONSTRAINT vendor_audit_logs_action_check;
ALTER TABLE vendor_audit_logs ADD CONSTRAINT vendor_audit_logs_action_check CHECK(action IN
 ('approved','rejected','suspended','resubmitted','restored','payout_submitted','payout_verified','payout_rejected','payout_disabled',
  'payout_details_read','event_replayed','shop_policy_approved','shop_policy_rejected',
  'return_destination_set','return_destination_verified','return_destination_rejected'));
