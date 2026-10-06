-- Stored images plus uploads in progress (lock the review first so
-- concurrent uploads queue up).
-- name: CountImageSlots :one
SELECT ((SELECT count(*) FROM review_images i WHERE i.review_id = @review_id::uuid)
    + (SELECT count(*) FROM review_image_uploads u WHERE u.review_id = @review_id::uuid AND u.parked_at IS NULL))::bigint AS slots;

-- name: AddUpload :exec
INSERT INTO review_image_uploads (review_id, object_key) VALUES ($1, $2);

-- name: InsertImage :one
INSERT INTO review_images (review_id, object_key, url, content_type, size_bytes, position)
SELECT @review_id::uuid, @object_key::text, @url::text, @content_type::text, @size_bytes::bigint, COALESCE(max(position) + 1, 0)
FROM review_images WHERE review_id = @review_id::uuid
RETURNING id, position, created_at;

-- name: DeleteUpload :exec
DELETE FROM review_image_uploads WHERE object_key = $1;

-- name: ClaimStaleUploads :many
UPDATE review_image_uploads u SET next_attempt_at = now() + interval '5 minutes'
WHERE u.object_key IN (SELECT object_key FROM review_image_uploads
    WHERE parked_at IS NULL AND next_attempt_at <= now() ORDER BY next_attempt_at LIMIT @batch_size::int FOR UPDATE SKIP LOCKED)
RETURNING u.object_key, u.attempts, EXISTS (SELECT 1 FROM review_images i WHERE i.object_key = u.object_key) AS recorded;

-- name: UploadCleanupFailed :exec
UPDATE review_image_uploads SET attempts = attempts + 1, last_error = @last_error::text, next_attempt_at = @next_attempt_at,
    parked_at = CASE WHEN @park::boolean THEN now() END
WHERE object_key = @object_key;
