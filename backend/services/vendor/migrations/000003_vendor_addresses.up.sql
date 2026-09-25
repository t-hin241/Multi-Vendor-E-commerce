-- A vendor may have one or more warehouse/pickup addresses. Exactly one
-- may be the default (enforced by the partial unique index below) — the
-- one used for operational/display purposes on a shipment.
CREATE TABLE vendor_addresses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id UUID NOT NULL REFERENCES vendors (id) ON DELETE CASCADE,
    recipient_name TEXT NOT NULL,
    phone TEXT NOT NULL,
    province TEXT NOT NULL,
    district TEXT NOT NULL,
    ward TEXT NOT NULL,
    street_address TEXT NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX vendor_addresses_vendor_id_idx ON vendor_addresses (vendor_id);
CREATE UNIQUE INDEX vendor_addresses_one_default_idx ON vendor_addresses (vendor_id) WHERE is_default;
