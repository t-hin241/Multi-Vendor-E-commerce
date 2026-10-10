-- PW-032: reimbursements are the marketplace's own disbursements to a
-- buyer outside a capture (e.g. a return shipping fee the buyer paid). A
-- separate book: no refund row, no refundable amount, no settlement entry.
-- Two different admins (prepare, approve); paid by bank transfer to the
-- buyer's verified refund account of the same order.
CREATE TABLE reimbursements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL,
    buyer_id UUID NOT NULL,
    reason_code TEXT NOT NULL CHECK (reason_code IN ('return_shipping_fee', 'goodwill', 'other')),
    reason TEXT NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
    amount BIGINT NOT NULL CHECK (amount > 0),
    currency TEXT NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    status TEXT NOT NULL DEFAULT 'requested' CHECK (status IN ('requested', 'approved', 'paid', 'rejected')),
    requested_by UUID NOT NULL,
    decided_by UUID,
    decided_at TIMESTAMPTZ,
    decision_reason TEXT CHECK (decision_reason IS NULL OR length(decision_reason) <= 500),
    destination_id UUID REFERENCES refund_destinations (id),
    bank_reference TEXT CHECK (bank_reference IS NULL OR length(bank_reference) BETWEEN 3 AND 100),
    bank_reference_key TEXT,
    paid_by UUID,
    paid_at TIMESTAMPTZ,
    idempotency_key TEXT CHECK (idempotency_key IS NULL OR length(idempotency_key) BETWEEN 8 AND 100),
    version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (decided_by IS NULL OR decided_by <> requested_by),
    CHECK (status NOT IN ('approved', 'paid', 'rejected') OR decided_by IS NOT NULL),
    CHECK (status <> 'paid' OR (destination_id IS NOT NULL AND bank_reference_key IS NOT NULL AND paid_by IS NOT NULL))
);
CREATE UNIQUE INDEX reimbursements_idempotency_idx ON reimbursements (requested_by, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX reimbursements_bank_reference_idx ON reimbursements (bank_reference_key) WHERE bank_reference_key IS NOT NULL;
CREATE INDEX reimbursements_queue_idx ON reimbursements (status, created_at);
CREATE INDEX reimbursements_order_idx ON reimbursements (order_id);

-- History is kept: a reimbursement is never deleted.
CREATE FUNCTION reimbursements_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'reimbursements are never deleted';
    END IF;
    IF OLD.status IN ('paid', 'rejected') AND NEW.status <> OLD.status THEN
        RAISE EXCEPTION 'a % reimbursement is final', OLD.status;
    END IF;
    IF NEW.amount <> OLD.amount OR NEW.currency <> OLD.currency OR NEW.buyer_id <> OLD.buyer_id OR NEW.requested_by <> OLD.requested_by THEN
        RAISE EXCEPTION 'a reimbursement keeps its amount, buyer and requester';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER reimbursements_guard BEFORE UPDATE OR DELETE ON reimbursements FOR EACH ROW EXECUTE FUNCTION reimbursements_guard();
