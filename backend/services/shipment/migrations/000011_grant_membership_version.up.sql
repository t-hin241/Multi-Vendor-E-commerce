-- PW-021: the shop membership version the seller's grant had when this
-- row was written (null: not written under a shop grant). A revoke that
-- lands between the authorize call and the commit stays visible: Vendor's
-- membership audit tells whether this version was already revoked.
ALTER TABLE shipment_tracking_events ADD COLUMN IF NOT EXISTS membership_version BIGINT CHECK (membership_version IS NULL OR membership_version >= 0);
