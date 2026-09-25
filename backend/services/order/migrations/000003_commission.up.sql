-- Commission rules are insert-only and versioned: setting a new rate never
-- touches an existing row, so a vendor order's commission snapshot (taken
-- at payment time) is never retroactively changed by a later rule change.
CREATE TABLE commission_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rate_bps INTEGER NOT NULL CHECK (rate_bps >= 0 AND rate_bps <= 10000),
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX commission_rules_created_at_idx ON commission_rules (created_at DESC);

-- A default rate so checkout/payment has a rule to snapshot from day one;
-- admin can add a new version at any time via the admin API.
INSERT INTO commission_rules (rate_bps) VALUES (1000);

-- Snapshotted at the moment a vendor order is marked paid, from whatever
-- commission_rules row was current then. NULL until paid.
ALTER TABLE vendor_orders
    ADD COLUMN commission_rate_bps INTEGER,
    ADD COLUMN commission_amount BIGINT,
    ADD COLUMN net_amount BIGINT;
