-- A user may now own more than one shop (1:N vendor <-> user) — drop the
-- constraint that capped it at one, keep a plain index for the same lookup
-- pattern's performance (listing every shop a user owns).
DROP INDEX vendors_user_id_key;
CREATE INDEX vendors_user_id_idx ON vendors (user_id);
