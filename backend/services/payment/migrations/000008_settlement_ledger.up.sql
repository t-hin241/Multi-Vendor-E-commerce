-- PAY-04: append-only settlement ledger, vendor-order snapshots from Order,
-- and manual payout batches whose items are backed by ledger entries.

-- Refunds name the vendor order they charge, so the ledger can debit the
-- right vendor. NULL for refunds that belong to no package (late or
-- duplicate captures).
ALTER TABLE payment_refunds ADD COLUMN vendor_order_id UUID;
CREATE INDEX payment_refunds_vendor_order_idx ON payment_refunds (vendor_order_id) WHERE vendor_order_id IS NOT NULL;

-- Order's completed vendor order, as Order snapshotted it at checkout.
-- Immutable: a correction is a ledger adjustment, never an edit.
CREATE TABLE settlement_vendor_orders (
    vendor_order_id UUID PRIMARY KEY,
    order_id UUID NOT NULL,
    vendor_id UUID NOT NULL,
    currency TEXT NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    subtotal_amount BIGINT NOT NULL CHECK (subtotal_amount >= 0),
    shipping_amount BIGINT NOT NULL CHECK (shipping_amount >= 0),
    commission_amount BIGINT NOT NULL CHECK (commission_amount >= 0 AND commission_amount <= subtotal_amount),
    commission_rate_bps INTEGER NOT NULL CHECK (commission_rate_bps BETWEEN 0 AND 10000),
    commission_rule_version BIGINT,
    completed_at TIMESTAMPTZ NOT NULL,
    -- End of the return window: sales become payable from here.
    eligible_at TIMESTAMPTZ NOT NULL,
    policy_version TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX settlement_vendor_orders_vendor_idx ON settlement_vendor_orders (vendor_id, completed_at);

-- What the marketplace owes each vendor. Signed amounts: sales and
-- shipping are credits; commission, refunds and payouts are debits. The
-- balance owed is the sum of all entries.
CREATE TABLE settlement_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id UUID NOT NULL,
    vendor_order_id UUID REFERENCES settlement_vendor_orders (vendor_order_id),
    entry_type TEXT NOT NULL CHECK (entry_type IN ('sale', 'shipping', 'commission', 'refund', 'commission_reversal', 'payout', 'adjustment')),
    amount BIGINT NOT NULL CHECK (amount <> 0),
    currency TEXT NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    -- For a refund: the part attributed to items (drives commission reversal).
    base_amount BIGINT CHECK (base_amount >= 0),
    -- Idempotency: one entry per (type, source).
    source_ref TEXT NOT NULL,
    eligible_at TIMESTAMPTZ NOT NULL,
    note TEXT CHECK (length(note) <= 500),
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entry_type, source_ref)
);
CREATE INDEX settlement_entries_vendor_idx ON settlement_entries (vendor_id, currency, created_at);
CREATE INDEX settlement_entries_vendor_order_idx ON settlement_entries (vendor_order_id);

CREATE FUNCTION settlement_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'settlement records are append-only; post an adjustment instead';
END $$;
CREATE TRIGGER settlement_entries_append_only BEFORE UPDATE OR DELETE ON settlement_entries
    FOR EACH ROW EXECUTE FUNCTION settlement_append_only();
CREATE TRIGGER settlement_vendor_orders_append_only BEFORE UPDATE OR DELETE ON settlement_vendor_orders
    FOR EACH ROW EXECUTE FUNCTION settlement_append_only();

-- Manual payout: one item per vendor per batch, backed by the ledger
-- entries it pays. Pre-existing per-vendor-order items keep their column.
ALTER TABLE payout_items ALTER COLUMN vendor_order_id DROP NOT NULL;
ALTER TABLE payout_items
    ADD COLUMN evidence_reference TEXT CHECK (length(evidence_reference) <= 200),
    ADD COLUMN note TEXT CHECK (length(note) <= 500),
    ADD COLUMN resolved_by UUID,
    ADD COLUMN resolved_at TIMESTAMPTZ;
ALTER TABLE payout_items ADD CONSTRAINT payout_items_succeeded_needs_evidence
    CHECK (status <> 'succeeded' OR provider_payout_id IS NOT NULL OR evidence_reference IS NOT NULL) NOT VALID;
-- 'completed': every item of the batch is resolved (succeeded or failed).
ALTER TABLE payout_batches DROP CONSTRAINT IF EXISTS payout_batches_status_check;
ALTER TABLE payout_batches ADD CONSTRAINT payout_batches_status_check
    CHECK (status IN ('pending', 'submitted', 'succeeded', 'failed', 'completed'));
ALTER TABLE payout_batches ADD COLUMN currency TEXT CHECK (currency ~ '^[A-Z]{3}$');

CREATE TABLE payout_item_entries (
    payout_item_id UUID NOT NULL REFERENCES payout_items (id),
    entry_id UUID NOT NULL REFERENCES settlement_entries (id),
    PRIMARY KEY (payout_item_id, entry_id)
);
CREATE INDEX payout_item_entries_entry_idx ON payout_item_entries (entry_id);
