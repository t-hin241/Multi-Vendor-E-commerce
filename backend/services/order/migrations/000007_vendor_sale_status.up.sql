CREATE TABLE vendor_sale_status (
 vendor_id UUID PRIMARY KEY, status TEXT NOT NULL CHECK(status IN ('pending','approved','rejected','suspended')),
 version BIGINT NOT NULL CHECK(version>0), confirmed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
