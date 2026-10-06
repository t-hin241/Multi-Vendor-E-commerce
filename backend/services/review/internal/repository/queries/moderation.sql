-- name: ListReasons :many
SELECT * FROM moderation_reasons WHERE (@active_only::boolean = false OR is_active) ORDER BY label;

-- name: GetReason :one
SELECT * FROM moderation_reasons WHERE id = $1;

-- name: CreateReason :one
INSERT INTO moderation_reasons (code, label, description) VALUES ($1, $2, $3)
RETURNING id, is_active, created_at, updated_at;

-- name: UpdateReason :one
UPDATE moderation_reasons SET code = @code, label = @label, description = @description, is_active = @is_active, updated_at = now()
WHERE id = @id RETURNING updated_at;

-- name: CreateReport :one
INSERT INTO review_reports (review_id, reporting_vendor_id, reason_id, reason_code, reason_label, note)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, status, created_at, updated_at;

-- name: ListReports :many
SELECT rr.* FROM review_reports rr JOIN reviews r ON r.id = rr.review_id
WHERE (@status::text = '' OR rr.status = @status::text) AND (@vendor_id::text = '' OR r.vendor_id::text = @vendor_id::text)
    AND (@product_id::text = '' OR r.product_id::text = @product_id::text)
ORDER BY rr.created_at DESC, rr.id LIMIT @page_limit::int OFFSET @page_offset::int;

-- name: GetReportForUpdate :one
SELECT * FROM review_reports WHERE id = $1 FOR UPDATE;

-- Closes an open report; 0 rows means it was not open.
-- name: ResolveReport :execrows
UPDATE review_reports SET status = 'resolved', decision = @decision::text, resolution_reason_id = @resolution_reason_id,
    resolved_by = @resolved_by::uuid, resolved_at = now(), resolution_note = @resolution_note, updated_at = now()
WHERE id = @id AND status = 'open';

-- name: HideReview :execrows
UPDATE reviews SET status = 'hidden', hidden_reason_id = @hidden_reason_id::uuid, hidden_by = @hidden_by::uuid,
    hidden_note = @hidden_note, hidden_at = now(), updated_at = now()
WHERE id = @id AND status = 'published';

-- name: RestoreReview :execrows
UPDATE reviews SET status = 'published', hidden_reason_id = NULL, hidden_by = NULL, hidden_note = NULL, hidden_at = NULL,
    updated_at = now()
WHERE id = $1 AND status = 'hidden';

-- name: InsertAudit :exec
INSERT INTO review_moderation_audit_logs (review_id, report_id, reason_id, actor_id, action, note, request_id, entity_type,
    entity_id, changes)
VALUES (@review_id, @report_id, @reason_id, @actor_id, @action, @note, @request_id, @entity_type, @entity_id, @changes);

-- The moderation and upload report (admin dashboard).
-- name: Operations :one
SELECT
    (SELECT count(*) FROM review_reports WHERE status = 'open')::bigint AS open_reports,
    (SELECT COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)) / 3600, 0)::bigint FROM review_reports WHERE status = 'open')::bigint
        AS oldest_open_report_hours,
    (SELECT count(*) FROM reviews WHERE status = 'hidden' AND hidden_at > now() - interval '7 days')::bigint AS hidden_7d,
    (SELECT count(*) FROM reviews WHERE created_at > now() - interval '24 hours')::bigint AS reviews_24h,
    (SELECT count(*) FROM review_image_uploads WHERE parked_at IS NULL AND created_at < now() - interval '1 hour')::bigint
        AS image_cleanup_pending,
    (SELECT count(*) FROM review_image_uploads WHERE parked_at IS NOT NULL)::bigint AS image_cleanup_parked,
    (SELECT count(*) FROM reviews WHERE author_label_checked_at IS NULL)::bigint AS author_labels_pending;
