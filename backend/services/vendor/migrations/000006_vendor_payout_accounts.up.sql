CREATE TABLE vendor_payout_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id UUID NOT NULL REFERENCES vendors(id) ON DELETE RESTRICT,
    bank_bin TEXT NOT NULL,
    account_number_ciphertext BYTEA NOT NULL,
    account_name_ciphertext BYTEA NOT NULL,
    account_number_last4 TEXT NOT NULL CHECK (length(account_number_last4) = 4),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'verified', 'rejected', 'disabled')),
    verified_by UUID,
    verified_at TIMESTAMPTZ,
    rejection_reason TEXT,
    is_default BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX vendor_payout_accounts_default_idx ON vendor_payout_accounts(vendor_id) WHERE is_default AND status = 'verified';
CREATE INDEX vendor_payout_accounts_vendor_idx ON vendor_payout_accounts(vendor_id, created_at DESC);
