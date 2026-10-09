-- PW-001: returns and refunds hold the vendor order's payout through
-- Payment's ledger (00 §6.1), like support cases (000019), paid
-- cancellations (000021) and delivery exceptions (000022). One hold per
-- source, prepared in the transaction that opens the return or refund and
-- acquired by an effect; released (by the hold sweep) once the source is
-- final. The legacy hold query stays until the backfill is reconciled.
CREATE TABLE source_settlement_holds (
    source_type TEXT NOT NULL CHECK (source_type IN ('return', 'refund')),
    source_id UUID NOT NULL,
    order_id UUID NOT NULL REFERENCES orders (id),
    vendor_id UUID NOT NULL,
    vendor_order_id UUID NOT NULL REFERENCES vendor_orders (id),
    hold_id UUID NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('preparing', 'active', 'needs_review', 'releasing', 'released')),
    note TEXT CHECK (note IS NULL OR length(note) <= 500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (source_type, source_id)
);
CREATE INDEX source_settlement_holds_open_idx ON source_settlement_holds (status, updated_at) WHERE status <> 'released';
CREATE INDEX source_settlement_holds_vendor_order_idx ON source_settlement_holds (vendor_order_id);
