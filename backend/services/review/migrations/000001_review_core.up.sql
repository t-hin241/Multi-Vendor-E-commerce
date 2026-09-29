CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE moderation_reasons (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL,
    description TEXT,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE reviews (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    buyer_id UUID NOT NULL,
    vendor_id UUID NOT NULL,
    product_id UUID NOT NULL,
    order_item_id UUID NOT NULL,
    vendor_order_id UUID NOT NULL,
    rating SMALLINT NOT NULL CHECK (rating BETWEEN 1 AND 5),
    comment TEXT NOT NULL CHECK (length(comment) BETWEEN 1 AND 2000),
    status TEXT NOT NULL DEFAULT 'published' CHECK (status IN ('published', 'hidden')),
    hidden_reason_id UUID REFERENCES moderation_reasons(id),
    hidden_by UUID,
    hidden_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (buyer_id, order_item_id)
);
CREATE INDEX reviews_product_published_idx ON reviews(product_id, created_at DESC) WHERE status = 'published';
CREATE INDEX reviews_vendor_idx ON reviews(vendor_id, created_at DESC);
CREATE INDEX reviews_buyer_idx ON reviews(buyer_id, created_at DESC);

CREATE TABLE review_images (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    review_id UUID NOT NULL REFERENCES reviews(id) ON DELETE CASCADE,
    object_key TEXT NOT NULL UNIQUE,
    url TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    position INTEGER NOT NULL CHECK (position >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (review_id, position)
);

CREATE TABLE review_replies (
    review_id UUID PRIMARY KEY REFERENCES reviews(id) ON DELETE CASCADE,
    vendor_id UUID NOT NULL,
    message TEXT NOT NULL CHECK (length(message) BETWEEN 1 AND 2000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE review_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    review_id UUID NOT NULL REFERENCES reviews(id) ON DELETE RESTRICT,
    reporting_vendor_id UUID NOT NULL,
    reason_id UUID NOT NULL REFERENCES moderation_reasons(id),
    reason_code TEXT NOT NULL,
    reason_label TEXT NOT NULL,
    note TEXT,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    decision TEXT CHECK (decision IN ('keep', 'hide')),
    resolution_reason_id UUID REFERENCES moderation_reasons(id),
    resolved_by UUID,
    resolved_at TIMESTAMPTZ,
    resolution_note TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX review_reports_open_vendor_idx ON review_reports(review_id, reporting_vendor_id) WHERE status = 'open';
CREATE INDEX review_reports_status_idx ON review_reports(status, created_at DESC);

CREATE TABLE review_moderation_audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    review_id UUID NOT NULL REFERENCES reviews(id) ON DELETE RESTRICT,
    report_id UUID REFERENCES review_reports(id) ON DELETE RESTRICT,
    reason_id UUID REFERENCES moderation_reasons(id),
    actor_id UUID NOT NULL,
    action TEXT NOT NULL,
    note TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX review_moderation_audit_logs_review_idx ON review_moderation_audit_logs(review_id, created_at DESC);
