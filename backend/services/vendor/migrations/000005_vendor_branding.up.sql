-- Shop branding: buyer-facing logo/banner images and a free-text policy
-- (return/shipping) section, shown on the new public shop page. Object keys
-- are stored alongside each URL so a replacement upload can delete the
-- superseded blob from object storage.
ALTER TABLE vendors
    ADD COLUMN logo_url TEXT,
    ADD COLUMN logo_object_key TEXT,
    ADD COLUMN banner_url TEXT,
    ADD COLUMN banner_object_key TEXT,
    ADD COLUMN policy_text TEXT NOT NULL DEFAULT '';
