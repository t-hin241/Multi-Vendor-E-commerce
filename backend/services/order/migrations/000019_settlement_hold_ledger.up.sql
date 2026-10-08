-- PW-001 (00 §6.1): a support case that may change what the vendor is owed
-- acquires a hold in Payment's ledger through a durable effect, and
-- releases it when the case closes. The case only counts as protecting the
-- money once the hold is active; until then it is "preparing" and a
-- financial resolution answers 503 hold_unavailable. needs_review: Payment
-- found the payout already claimed (or refused the hold) — an operator must
-- recover it. The legacy HeldForSettlement query keeps answering too.
ALTER TABLE support_cases
    ADD COLUMN hold_id UUID UNIQUE,
    ADD COLUMN hold_status TEXT CHECK (hold_status IN ('preparing', 'active', 'needs_review', 'releasing', 'released')),
    ADD COLUMN hold_note TEXT CHECK (hold_note IS NULL OR length(hold_note) <= 500),
    ADD COLUMN hold_updated_at TIMESTAMPTZ,
    ADD CONSTRAINT support_cases_hold_pair CHECK ((hold_id IS NULL) = (hold_status IS NULL));
CREATE INDEX support_cases_hold_attention_idx ON support_cases (hold_updated_at)
    WHERE hold_status IN ('preparing', 'needs_review', 'releasing');
CREATE INDEX support_cases_hold_missing_idx ON support_cases (created_at)
    WHERE financial_hold AND hold_id IS NULL AND status <> 'closed';

ALTER TABLE order_effects DROP CONSTRAINT IF EXISTS order_effects_kind_check;
ALTER TABLE order_effects ADD CONSTRAINT order_effects_kind_check CHECK (kind IN (
    'create_shipment', 'cancel_shipment', 'release_inventory', 'notify', 'request_refund', 'restock_return', 'settle_vendor_order',
    'report_rejected_outcome', 'acquire_settlement_hold', 'release_settlement_hold'));
