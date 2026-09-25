DROP INDEX vendors_user_id_idx;
CREATE UNIQUE INDEX vendors_user_id_key ON vendors (user_id);
