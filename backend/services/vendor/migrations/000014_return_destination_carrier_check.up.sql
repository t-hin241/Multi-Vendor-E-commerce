-- PW-042: the carrier's address check of a return destination, asked
-- through Shipment (which owns the carrier integration) for each version.
-- A deliverable answer verifies the version (verified_by stays NULL: no
-- person); an undeliverable one rejects it with the carrier's reason; no
-- address check at the carrier leaves it to an admin. An admin can still
-- decide any version.
ALTER TABLE vendor_return_destinations
    ADD COLUMN carrier_check_version BIGINT,
    ADD COLUMN carrier_check_result TEXT CHECK (carrier_check_result IN ('deliverable', 'undeliverable', 'unsupported')),
    ADD COLUMN carrier_check_reason TEXT CHECK (carrier_check_reason IS NULL OR length(carrier_check_reason) <= 300),
    ADD COLUMN carrier_check_ref TEXT CHECK (carrier_check_ref IS NULL OR length(carrier_check_ref) <= 200),
    ADD COLUMN carrier_checked_at TIMESTAMPTZ,
    ADD CONSTRAINT vendor_return_destinations_carrier_check
        CHECK ((carrier_check_version IS NULL) = (carrier_check_result IS NULL) AND (carrier_check_version IS NULL) = (carrier_checked_at IS NULL));
CREATE INDEX vendor_return_destinations_carrier_pending_idx ON vendor_return_destinations (updated_at)
    WHERE verified_version IS DISTINCT FROM version AND carrier_check_version IS DISTINCT FROM version;

ALTER TABLE vendor_audit_logs DROP CONSTRAINT vendor_audit_logs_action_check;
ALTER TABLE vendor_audit_logs ADD CONSTRAINT vendor_audit_logs_action_check CHECK(action IN
 ('approved','rejected','suspended','resubmitted','restored','payout_submitted','payout_verified','payout_rejected','payout_disabled',
  'payout_details_read','event_replayed','shop_policy_approved','shop_policy_rejected',
  'return_destination_set','return_destination_verified','return_destination_rejected',
  'return_destination_carrier_verified','return_destination_carrier_rejected'));
