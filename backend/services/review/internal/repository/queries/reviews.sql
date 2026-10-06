-- name: CreateReview :one
INSERT INTO reviews (buyer_id, vendor_id, product_id, order_item_id, vendor_order_id, rating, comment, verified_purchase,
    author_label, author_label_checked_at)
VALUES (@buyer_id, @vendor_id, @product_id, @order_item_id, @vendor_order_id, @rating, @comment, @verified_purchase,
    @author_label, CASE WHEN @label_checked::boolean THEN now() END)
RETURNING *;

-- name: GetReview :one
SELECT * FROM reviews WHERE id = $1;

-- name: GetReviewForUpdate :one
SELECT * FROM reviews WHERE id = $1 FOR UPDATE;

-- name: ReviewedItemIDs :many
SELECT order_item_id FROM reviews WHERE buyer_id = $1;

-- name: ListPublicReviews :many
SELECT * FROM reviews
WHERE product_id = @product_id AND status = 'published' AND (verified_purchase OR @include_unverified::boolean)
    AND (@rating::int = 0 OR rating = @rating::int)
ORDER BY created_at DESC, id LIMIT @page_limit::int OFFSET @page_offset::int;

-- name: ListBuyerReviews :many
SELECT * FROM reviews WHERE buyer_id = @buyer_id
ORDER BY created_at DESC, id LIMIT @page_limit::int OFFSET @page_offset::int;

-- replied: nil = all, true = with a shop reply, false = without one.
-- name: ListVendorReviews :many
SELECT r.* FROM reviews r
WHERE r.vendor_id = @vendor_id AND r.status = 'published' AND (r.verified_purchase OR @include_unverified::boolean)
    AND (@product_id::text = '' OR r.product_id::text = @product_id::text) AND (@rating::int = 0 OR r.rating = @rating::int)
    AND (sqlc.narg(replied)::boolean IS NULL
        OR sqlc.narg(replied)::boolean = EXISTS (SELECT 1 FROM review_replies rr WHERE rr.review_id = r.id))
ORDER BY r.created_at DESC, r.id LIMIT @page_limit::int OFFSET @page_offset::int;

-- name: ListAdminReviews :many
SELECT * FROM reviews
WHERE (@buyer_id::text = '' OR buyer_id::text = @buyer_id::text) AND (@vendor_id::text = '' OR vendor_id::text = @vendor_id::text)
    AND (@product_id::text = '' OR product_id::text = @product_id::text) AND (@status::text = '' OR status = @status::text)
    AND (@rating::int = 0 OR rating = @rating::int)
ORDER BY created_at DESC, id LIMIT @page_limit::int OFFSET @page_offset::int;

-- The two summaries count exactly what the public lists can show; they
-- differ only in the scope column (each has its own index).

-- name: ProductSummary :one
SELECT COALESCE(avg(rating), 0)::float8 AS rating_average, count(*) AS rating_count,
    count(*) FILTER (WHERE rating = 1) AS rated_1, count(*) FILTER (WHERE rating = 2) AS rated_2,
    count(*) FILTER (WHERE rating = 3) AS rated_3, count(*) FILTER (WHERE rating = 4) AS rated_4,
    count(*) FILTER (WHERE rating = 5) AS rated_5
FROM reviews WHERE product_id = @product_id AND status = 'published' AND (verified_purchase OR @include_unverified::boolean);

-- name: VendorSummary :one
SELECT COALESCE(avg(rating), 0)::float8 AS rating_average, count(*) AS rating_count,
    count(*) FILTER (WHERE rating = 1) AS rated_1, count(*) FILTER (WHERE rating = 2) AS rated_2,
    count(*) FILTER (WHERE rating = 3) AS rated_3, count(*) FILTER (WHERE rating = 4) AS rated_4,
    count(*) FILTER (WHERE rating = 5) AS rated_5
FROM reviews WHERE vendor_id = @vendor_id AND status = 'published' AND (verified_purchase OR @include_unverified::boolean);

-- name: ImagesForReviews :many
SELECT * FROM review_images WHERE review_id = ANY(@review_ids::uuid[]) ORDER BY review_id, position;

-- name: RepliesForReviews :many
SELECT * FROM review_replies WHERE review_id = ANY(@review_ids::uuid[]);

-- name: LockReplyMessage :one
SELECT message FROM review_replies WHERE review_id = $1 FOR UPDATE;

-- name: UpsertReply :one
INSERT INTO review_replies (review_id, vendor_id, message) VALUES ($1, $2, $3)
ON CONFLICT (review_id) DO UPDATE SET message = excluded.message, updated_at = now()
RETURNING created_at, updated_at;

-- name: PendingLabels :many
SELECT id AS review_id, buyer_id FROM reviews WHERE author_label_checked_at IS NULL
ORDER BY created_at LIMIT @batch_size::int;

-- name: SetLabel :exec
UPDATE reviews SET author_label = @author_label, author_label_checked_at = now() WHERE id = @id;
