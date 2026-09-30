-- INV-04: vendor physical counts (kiểm kê). A count only lowers available
-- stock; reserved units are never touched. The id is supplied by the client
-- so a retried request is recognized instead of applied twice.
CREATE TABLE inventory_stock_counts (
 id UUID PRIMARY KEY,
 inventory_item_id UUID NOT NULL REFERENCES inventory_items(id),
 counted_on_hand BIGINT NOT NULL CHECK(counted_on_hand >= 0),
 previous_available BIGINT NOT NULL CHECK(previous_available >= 0),
 new_available BIGINT NOT NULL CHECK(new_available >= 0 AND new_available <= previous_available),
 reserved_at_count BIGINT NOT NULL CHECK(reserved_at_count >= 0),
 actor_user_id UUID NOT NULL,
 reason TEXT NOT NULL CHECK(length(reason) BETWEEN 1 AND 500),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK(counted_on_hand = new_available + reserved_at_count)
);
CREATE INDEX inventory_stock_counts_item_idx ON inventory_stock_counts(inventory_item_id, created_at DESC);
