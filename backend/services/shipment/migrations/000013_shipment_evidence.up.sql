-- PW-038: evidence of a failure report (lost or returned package), owned by
-- Shipment; the files live in the shared object storage (private bucket
-- SHIPMENT_EVIDENCE_BUCKET). A file is uploaded for one shipment and
-- attached once, by its owner's report; one never attached is removed
-- after a day.
CREATE TABLE shipment_evidence (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id UUID NOT NULL,
    owner_role TEXT NOT NULL CHECK (owner_role IN ('vendor', 'admin')),
    shipment_id UUID NOT NULL REFERENCES shipments (id),
    report_kind TEXT CHECK (report_kind IN ('lost', 'returned')),
    object_key TEXT NOT NULL UNIQUE CHECK (length(object_key) BETWEEN 1 AND 200),
    content_type TEXT NOT NULL CHECK (content_type IN ('image/jpeg', 'image/png', 'video/mp4')),
    size_bytes BIGINT NOT NULL CHECK (size_bytes BETWEEN 1 AND 52428800),
    state TEXT NOT NULL DEFAULT 'uploaded' CHECK (state IN ('uploaded', 'attached', 'deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    attached_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    CHECK ((state = 'attached') = (report_kind IS NOT NULL) OR state = 'deleted')
);
CREATE INDEX shipment_evidence_shipment_idx ON shipment_evidence (shipment_id) WHERE state = 'attached';
CREATE INDEX shipment_evidence_orphan_idx ON shipment_evidence (created_at) WHERE state = 'uploaded';
