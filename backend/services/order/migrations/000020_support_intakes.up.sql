-- PW-012 (AF-01 §1): a buyer who cannot find the order (paid, but no order
-- shows) sends a support intake with the checkout, payment or bank
-- transfer reference. An admin looks it up and links it to an order that
-- is verified to belong to the same buyer: Order then opens (or adds to)
-- the support case. The client never names the order.
CREATE TABLE support_intakes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    buyer_id UUID NOT NULL,
    reference_kind TEXT NOT NULL CHECK (reference_kind IN ('checkout', 'payment', 'bank_transfer')),
    reference TEXT NOT NULL CHECK (length(reference) BETWEEN 3 AND 100),
    message TEXT NOT NULL CHECK (length(message) BETWEEN 1 AND 4000),
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'linked', 'closed')),
    linked_case_id UUID REFERENCES support_cases (id),
    handled_by UUID,
    handled_at TIMESTAMPTZ,
    close_reason TEXT CHECK (close_reason IS NULL OR length(close_reason) BETWEEN 1 AND 500),
    idempotency_key TEXT CHECK (idempotency_key IS NULL OR idempotency_key ~ '^[A-Za-z0-9._:-]{8,100}$'),
    request_hash TEXT,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((status = 'linked') = (linked_case_id IS NOT NULL)),
    CHECK (status = 'open' OR (handled_by IS NOT NULL AND handled_at IS NOT NULL)),
    CHECK (status <> 'closed' OR close_reason IS NOT NULL)
);
-- One open intake per buyer and reference.
CREATE UNIQUE INDEX support_intakes_open_reference ON support_intakes (buyer_id, reference_kind, upper(reference)) WHERE status = 'open';
CREATE UNIQUE INDEX support_intakes_idempotency_key ON support_intakes (buyer_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX support_intakes_queue_idx ON support_intakes (status, created_at, id);
CREATE INDEX support_intakes_buyer_idx ON support_intakes (buyer_id, created_at DESC);

-- A handled intake is final, and none is ever deleted.
CREATE FUNCTION support_intake_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'support intakes are never deleted';
    END IF;
    IF OLD.status <> 'open' THEN
        RAISE EXCEPTION 'a % support intake is final', OLD.status;
    END IF;
    IF NEW.buyer_id <> OLD.buyer_id OR NEW.reference <> OLD.reference OR NEW.message <> OLD.message THEN
        RAISE EXCEPTION 'a support intake keeps what the buyer sent';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER support_intake_guard BEFORE UPDATE OR DELETE ON support_intakes
FOR EACH ROW EXECUTE FUNCTION support_intake_guard();

ALTER TABLE order_admin_audit DROP CONSTRAINT IF EXISTS order_admin_audit_entity_type_check;
ALTER TABLE order_admin_audit ADD CONSTRAINT order_admin_audit_entity_type_check
    CHECK (entity_type IN ('order', 'refund', 'return_request', 'effect', 'commission_rule', 'support_case', 'support_intake'));
