-- Admin-managed reference data: carriers and shipping zones. Plain
-- CRUD, not versioned — these are structural entities, not rules whose
-- historical value needs preserving (that's what shipping_fee_rules is for).
CREATE TABLE carriers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    code TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX carriers_code_key ON carriers (code);

CREATE TABLE shipping_zones (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    code TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX shipping_zones_code_key ON shipping_zones (code);

-- A province belongs to at most one zone at a time — enforced by the
-- unique index on province_code alone, not just the (zone_id, province_code)
-- pair.
CREATE TABLE shipping_zone_provinces (
    zone_id UUID NOT NULL REFERENCES shipping_zones (id) ON DELETE CASCADE,
    province_code TEXT NOT NULL,
    PRIMARY KEY (zone_id, province_code)
);
CREATE UNIQUE INDEX shipping_zone_provinces_province_key ON shipping_zone_provinces (province_code);

-- Insert-only, versioned per (carrier_id, zone_id) — same convention as
-- catalog's category_attribute_rules / order's commission_rules: editing a
-- fee rule inserts version+1, never updates/deletes the old row, so a
-- shipment already created against an older version keeps its snapshot
-- meaning intact.
CREATE TABLE shipping_fee_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    carrier_id UUID NOT NULL REFERENCES carriers (id) ON DELETE CASCADE,
    zone_id UUID NOT NULL REFERENCES shipping_zones (id) ON DELETE CASCADE,
    version INT NOT NULL,
    base_fee_amount BIGINT NOT NULL CHECK (base_fee_amount >= 0),
    free_weight_grams BIGINT NOT NULL DEFAULT 0 CHECK (free_weight_grams >= 0),
    extra_fee_per_kg BIGINT NOT NULL DEFAULT 0 CHECK (extra_fee_per_kg >= 0),
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX shipping_fee_rules_version_key ON shipping_fee_rules (carrier_id, zone_id, version);
CREATE INDEX shipping_fee_rules_current_idx ON shipping_fee_rules (carrier_id, zone_id, version DESC);

-- A vendor enabling one of the admin's carriers for their own shop.
-- vendor_id is a plain opaque UUID, not a cross-service FK — same pattern
-- as shipments.vendor_id already used below.
CREATE TABLE vendor_shipping_methods (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id UUID NOT NULL,
    carrier_id UUID NOT NULL REFERENCES carriers (id) ON DELETE CASCADE,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX vendor_shipping_methods_vendor_carrier_key ON vendor_shipping_methods (vendor_id, carrier_id);
CREATE UNIQUE INDEX vendor_shipping_methods_one_default_idx ON vendor_shipping_methods (vendor_id) WHERE is_default;

-- Automatic audit-on-mutation trail of every status change a shipment goes
-- through — same idea as inventory's stock_movements: a timeline the buyer
-- and vendor can both read, populated as a side effect of Advance, not
-- authored directly.
CREATE TABLE shipment_tracking_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shipment_id UUID NOT NULL REFERENCES shipments (id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    note TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX shipment_tracking_events_shipment_id_idx ON shipment_tracking_events (shipment_id, created_at);

-- Shipment gains: the buyer it belongs to (denormalized, so buyer-facing
-- reads never need a live cross-service ownership check), the resolved
-- carrier/zone/fee-rule used to compute its fee, the fee itself, the
-- package weight the fee was computed from, and a snapshot of the buyer's
-- destination — all immutable after creation, matching the codebase's
-- "snapshot at the boundary, never re-derive" convention.
ALTER TABLE shipments
    ADD COLUMN buyer_id UUID,
    ADD COLUMN carrier_id UUID REFERENCES carriers (id),
    ADD COLUMN zone_id UUID REFERENCES shipping_zones (id),
    ADD COLUMN zone_name TEXT,
    ADD COLUMN fee_rule_id UUID REFERENCES shipping_fee_rules (id),
    ADD COLUMN fee_amount BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN package_weight_grams BIGINT,
    ADD COLUMN recipient_name TEXT,
    ADD COLUMN phone TEXT,
    ADD COLUMN province TEXT,
    ADD COLUMN district TEXT,
    ADD COLUMN ward TEXT,
    ADD COLUMN street_address TEXT;

CREATE INDEX shipments_buyer_id_idx ON shipments (buyer_id);

ALTER TABLE shipments DROP CONSTRAINT shipments_status_check;
ALTER TABLE shipments ADD CONSTRAINT shipments_status_check
    CHECK (status IN ('pending', 'ready_to_ship', 'shipped', 'delivered', 'cancelled'));
