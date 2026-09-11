CREATE TABLE attributes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code TEXT NOT NULL,
    name TEXT NOT NULL,
    data_type TEXT NOT NULL CHECK (data_type IN ('text', 'number', 'boolean', 'select', 'multi_select')),
    unit TEXT,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX attributes_code_key ON attributes (code);

CREATE TABLE attribute_options (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    attribute_id UUID NOT NULL REFERENCES attributes (id) ON DELETE CASCADE,
    value TEXT NOT NULL,
    position INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX attribute_options_attribute_id_value_key ON attribute_options (attribute_id, value);

-- Insert-only, versioned per (category_id, attribute_id): editing a rule
-- inserts version+1, never updates/deletes the old row — same convention
-- as order's commission_rules, so a product_attribute_values row's rule_id
-- reference is never retroactively changed by a later rule edit.
CREATE TABLE category_attribute_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category_id UUID NOT NULL REFERENCES categories (id) ON DELETE CASCADE,
    attribute_id UUID NOT NULL REFERENCES attributes (id) ON DELETE CASCADE,
    version INT NOT NULL,
    is_required BOOLEAN NOT NULL DEFAULT FALSE,
    is_excluded BOOLEAN NOT NULL DEFAULT FALSE,
    position INT NOT NULL DEFAULT 0,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX category_attribute_rules_version_key ON category_attribute_rules (category_id, attribute_id, version);
CREATE INDEX category_attribute_rules_current_idx ON category_attribute_rules (category_id, attribute_id, version DESC);

-- Replaced wholesale per product (delete+insert in one transaction, same
-- convention as product_images.ReplaceForProduct) — never updated in place.
CREATE TABLE product_attribute_values (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id UUID NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    attribute_id UUID NOT NULL REFERENCES attributes (id),
    rule_id UUID REFERENCES category_attribute_rules (id),
    option_id UUID REFERENCES attribute_options (id),
    value_text TEXT,
    value_number NUMERIC,
    value_boolean BOOLEAN,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX product_attribute_values_product_id_idx ON product_attribute_values (product_id);
