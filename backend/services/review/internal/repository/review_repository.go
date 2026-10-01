package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"shopee/backend/pkg/middleware"
	"shopee/backend/services/review/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("review repository: not found")
var ErrConflict = errors.New("review repository: conflict")

type ReviewRepository struct{ pool *pgxpool.Pool }

func NewReviewRepository(pool *pgxpool.Pool) *ReviewRepository { return &ReviewRepository{pool: pool} }

const reviewColumns = `id, buyer_id, vendor_id, product_id, order_item_id, vendor_order_id, rating, comment, status, verified_purchase,
	author_label, hidden_reason_id, hidden_by, hidden_note, hidden_at, created_at, updated_at`

type scanner interface{ Scan(...any) error }

func scanReview(row scanner) (*domain.Review, error) {
	var v domain.Review
	err := row.Scan(&v.ID, &v.BuyerID, &v.VendorID, &v.ProductID, &v.OrderItemID, &v.VendorOrderID, &v.Rating, &v.Comment, &v.Status,
		&v.VerifiedPurchase, &v.AuthorLabel, &v.HiddenReasonID, &v.HiddenBy, &v.HiddenNote, &v.HiddenAt, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// Create records a review; labelChecked says whether Identity answered
// (the label may still be nil for an unknown user). A second review of the
// same order item is ErrConflict, also under concurrent requests.
func (r *ReviewRepository) Create(ctx context.Context, v *domain.Review, labelChecked bool) error {
	stored, err := scanReview(connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO reviews (buyer_id, vendor_id, product_id, order_item_id, vendor_order_id, rating, comment, verified_purchase,
			author_label, author_label_checked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, CASE WHEN $10 THEN now() END)
		RETURNING `+reviewColumns,
		v.BuyerID, v.VendorID, v.ProductID, v.OrderItemID, v.VendorOrderID, v.Rating, v.Comment, v.VerifiedPurchase, v.AuthorLabel, labelChecked))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	*v = *stored
	return nil
}

func (r *ReviewRepository) Find(ctx context.Context, id string) (*domain.Review, error) {
	return scanReview(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+reviewColumns+` FROM reviews WHERE id = $1`, id))
}

// FindForUpdate locks the review for the caller's transaction.
func (r *ReviewRepository) FindForUpdate(ctx context.Context, id string) (*domain.Review, error) {
	return scanReview(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+reviewColumns+` FROM reviews WHERE id = $1 FOR UPDATE`, id))
}

// ReviewedItems is the set of order items the buyer already reviewed.
func (r *ReviewRepository) ReviewedItems(ctx context.Context, buyerID string) (map[string]bool, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT order_item_id::text FROM reviews WHERE buyer_id = $1`, buyerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// ListPublic is the storefront list: published reviews, and only verified
// purchases unless includeUnverified (local/staging demo data).
func (r *ReviewRepository) ListPublic(ctx context.Context, productID string, rating int, includeUnverified bool, limit, offset int) ([]*domain.Review, error) {
	return r.list(ctx, `SELECT `+reviewColumns+` FROM reviews WHERE product_id = $1 AND status = 'published' AND (verified_purchase OR $2)
		AND ($3 = 0 OR rating = $3) ORDER BY created_at DESC, id LIMIT $4 OFFSET $5`, productID, includeUnverified, rating, limit, offset)
}

func (r *ReviewRepository) ListBuyer(ctx context.Context, buyerID string, limit, offset int) ([]*domain.Review, error) {
	return r.list(ctx, `SELECT `+reviewColumns+` FROM reviews WHERE buyer_id = $1 ORDER BY created_at DESC, id LIMIT $2 OFFSET $3`, buyerID, limit, offset)
}

func (r *ReviewRepository) ListVendor(ctx context.Context, vendorID, productID string, rating int, replied *bool, includeUnverified bool, limit, offset int) ([]*domain.Review, error) {
	repliedValue := -1
	if replied != nil {
		repliedValue = 0
		if *replied {
			repliedValue = 1
		}
	}
	return r.list(ctx, `SELECT `+reviewColumns+` FROM reviews r WHERE r.vendor_id = $1 AND r.status = 'published' AND (r.verified_purchase OR $7)
		AND ($2 = '' OR r.product_id::text = $2) AND ($3 = 0 OR r.rating = $3)
		AND ($4 = -1 OR ($4 = 1) = EXISTS (SELECT 1 FROM review_replies rr WHERE rr.review_id = r.id))
		ORDER BY r.created_at DESC, r.id LIMIT $5 OFFSET $6`, vendorID, productID, rating, repliedValue, limit, offset, includeUnverified)
}

// AdminFilter narrows the moderation list.
type AdminFilter struct {
	BuyerID, VendorID, ProductID, Status string
	Rating                               int
}

func (r *ReviewRepository) ListAdmin(ctx context.Context, f AdminFilter, limit, offset int) ([]*domain.Review, error) {
	return r.list(ctx, `SELECT `+reviewColumns+` FROM reviews WHERE ($1 = '' OR buyer_id::text = $1) AND ($2 = '' OR vendor_id::text = $2)
		AND ($3 = '' OR product_id::text = $3) AND ($4 = '' OR status = $4) AND ($5 = 0 OR rating = $5)
		ORDER BY created_at DESC, id LIMIT $6 OFFSET $7`, f.BuyerID, f.VendorID, f.ProductID, f.Status, f.Rating, limit, offset)
}

func (r *ReviewRepository) list(ctx context.Context, q string, args ...any) ([]*domain.Review, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*domain.Review, 0)
	for rows.Next() {
		v, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

const summarySelect = `SELECT COALESCE(avg(rating), 0), count(*), count(*) FILTER (WHERE rating = 1), count(*) FILTER (WHERE rating = 2),
	count(*) FILTER (WHERE rating = 3), count(*) FILTER (WHERE rating = 4), count(*) FILTER (WHERE rating = 5) FROM reviews`

func scanSummary(row pgx.Row) (domain.Summary, error) {
	var s domain.Summary
	err := row.Scan(&s.RatingAverage, &s.RatingCount, &s.Distribution[0], &s.Distribution[1], &s.Distribution[2], &s.Distribution[3], &s.Distribution[4])
	return s, err
}

// Summary counts exactly what the public list can show.
func (r *ReviewRepository) Summary(ctx context.Context, productID string, includeUnverified bool) (domain.Summary, error) {
	return scanSummary(connection(ctx, r.pool).QueryRow(ctx, summarySelect+` WHERE product_id = $1 AND status = 'published' AND (verified_purchase OR $2)`, productID, includeUnverified))
}

func (r *ReviewRepository) VendorSummary(ctx context.Context, vendorID string, includeUnverified bool) (domain.Summary, error) {
	return scanSummary(connection(ctx, r.pool).QueryRow(ctx, summarySelect+` WHERE vendor_id = $1 AND status = 'published' AND (verified_purchase OR $2)`, vendorID, includeUnverified))
}

// ImagesFor returns the images of several reviews in one query.
func (r *ReviewRepository) ImagesFor(ctx context.Context, reviewIDs []string) (map[string][]*domain.Image, error) {
	out := map[string][]*domain.Image{}
	if len(reviewIDs) == 0 {
		return out, nil
	}
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT id, review_id, object_key, url, content_type, size_bytes, position, created_at
		FROM review_images WHERE review_id = ANY($1::uuid[]) ORDER BY review_id, position`, reviewIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var i domain.Image
		if err := rows.Scan(&i.ID, &i.ReviewID, &i.ObjectKey, &i.URL, &i.ContentType, &i.SizeBytes, &i.Position, &i.CreatedAt); err != nil {
			return nil, err
		}
		out[i.ReviewID] = append(out[i.ReviewID], &i)
	}
	return out, rows.Err()
}

// RepliesFor returns the shop replies of several reviews in one query.
func (r *ReviewRepository) RepliesFor(ctx context.Context, reviewIDs []string) (map[string]*domain.Reply, error) {
	out := map[string]*domain.Reply{}
	if len(reviewIDs) == 0 {
		return out, nil
	}
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT review_id, vendor_id, message, created_at, updated_at FROM review_replies
		WHERE review_id = ANY($1::uuid[])`, reviewIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var x domain.Reply
		if err := rows.Scan(&x.ReviewID, &x.VendorID, &x.Message, &x.CreatedAt, &x.UpdatedAt); err != nil {
			return nil, err
		}
		out[x.ReviewID] = &x
	}
	return out, rows.Err()
}

// SetReply writes the shop's reply and returns the previous message (nil
// for a first reply), in the caller's transaction.
func (r *ReviewRepository) SetReply(ctx context.Context, reply *domain.Reply) (*string, error) {
	q := connection(ctx, r.pool)
	var previous *string
	err := q.QueryRow(ctx, `SELECT message FROM review_replies WHERE review_id = $1 FOR UPDATE`, reply.ReviewID).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	err = q.QueryRow(ctx, `INSERT INTO review_replies (review_id, vendor_id, message) VALUES ($1, $2, $3)
		ON CONFLICT (review_id) DO UPDATE SET message = excluded.message, updated_at = now() RETURNING created_at, updated_at`,
		reply.ReviewID, reply.VendorID, reply.Message).Scan(&reply.CreatedAt, &reply.UpdatedAt)
	return previous, err
}

// CountImageSlots counts stored images plus uploads in progress of a
// review (lock the review first so concurrent uploads queue up).
func (r *ReviewRepository) CountImageSlots(ctx context.Context, reviewID string) (int, error) {
	var n int
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT (SELECT count(*) FROM review_images WHERE review_id = $1)
		+ (SELECT count(*) FROM review_image_uploads WHERE review_id = $1 AND parked_at IS NULL)`, reviewID).Scan(&n)
	return n, err
}

// AddUpload records that objectKey is about to be stored for reviewID.
func (r *ReviewRepository) AddUpload(ctx context.Context, reviewID, objectKey string) error {
	_, err := connection(ctx, r.pool).Exec(ctx, `INSERT INTO review_image_uploads (review_id, object_key) VALUES ($1, $2)`, reviewID, objectKey)
	return err
}

// RecordImage stores the image row at the next position and closes its
// upload, in the caller's transaction (review locked).
func (r *ReviewRepository) RecordImage(ctx context.Context, image *domain.Image) error {
	q := connection(ctx, r.pool)
	err := q.QueryRow(ctx, `INSERT INTO review_images (review_id, object_key, url, content_type, size_bytes, position)
		SELECT $1, $2, $3, $4, $5, COALESCE(max(position) + 1, 0) FROM review_images WHERE review_id = $1
		RETURNING id, position, created_at`, image.ReviewID, image.ObjectKey, image.URL, image.ContentType, image.SizeBytes).
		Scan(&image.ID, &image.Position, &image.CreatedAt)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `DELETE FROM review_image_uploads WHERE object_key = $1`, image.ObjectKey)
	return err
}

// DeleteUpload closes an upload whose object is gone (or was never stored).
func (r *ReviewRepository) DeleteUpload(ctx context.Context, objectKey string) error {
	_, err := connection(ctx, r.pool).Exec(ctx, `DELETE FROM review_image_uploads WHERE object_key = $1`, objectKey)
	return err
}

// Upload is an upload left behind that needs its object removed.
type Upload struct {
	ObjectKey string
	Attempts  int
	Recorded  bool // an image row uses the object: only the intent goes
}

// ClaimStaleUploads takes up to limit uploads that are due, holding each
// for five minutes so another instance does not take it too.
func (r *ReviewRepository) ClaimStaleUploads(ctx context.Context, limit int) ([]Upload, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `
		UPDATE review_image_uploads u SET next_attempt_at = now() + interval '5 minutes'
		WHERE u.object_key IN (SELECT object_key FROM review_image_uploads
			WHERE parked_at IS NULL AND next_attempt_at <= now() ORDER BY next_attempt_at LIMIT $1 FOR UPDATE SKIP LOCKED)
		RETURNING u.object_key, u.attempts, EXISTS (SELECT 1 FROM review_images i WHERE i.object_key = u.object_key)`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Upload{}
	for rows.Next() {
		var u Upload
		if err := rows.Scan(&u.ObjectKey, &u.Attempts, &u.Recorded); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UploadCleanupFailed schedules another try, or parks the upload.
func (r *ReviewRepository) UploadCleanupFailed(ctx context.Context, objectKey, reason string, next time.Time, park bool) error {
	if len(reason) > 300 {
		reason = reason[:300]
	}
	_, err := connection(ctx, r.pool).Exec(ctx, `UPDATE review_image_uploads SET attempts = attempts + 1, last_error = $2, next_attempt_at = $3,
		parked_at = CASE WHEN $4 THEN now() END WHERE object_key = $1`, objectKey, reason, next, park)
	return err
}

func (r *ReviewRepository) ListReasons(ctx context.Context, activeOnly bool) ([]*domain.Reason, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT id, code, label, description, is_active, created_at, updated_at FROM moderation_reasons
		WHERE ($1 = false OR is_active) ORDER BY label`, activeOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*domain.Reason, 0)
	for rows.Next() {
		var x domain.Reason
		if err := rows.Scan(&x.ID, &x.Code, &x.Label, &x.Description, &x.IsActive, &x.CreatedAt, &x.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &x)
	}
	return out, rows.Err()
}

func (r *ReviewRepository) FindReason(ctx context.Context, id string) (*domain.Reason, error) {
	var x domain.Reason
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT id, code, label, description, is_active, created_at, updated_at FROM moderation_reasons WHERE id = $1`, id).
		Scan(&x.ID, &x.Code, &x.Label, &x.Description, &x.IsActive, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &x, nil
}

func (r *ReviewRepository) CreateReason(ctx context.Context, x *domain.Reason) error {
	err := connection(ctx, r.pool).QueryRow(ctx, `INSERT INTO moderation_reasons (code, label, description) VALUES ($1, $2, $3)
		RETURNING id, is_active, created_at, updated_at`, x.Code, x.Label, x.Description).Scan(&x.ID, &x.IsActive, &x.CreatedAt, &x.UpdatedAt)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

func (r *ReviewRepository) UpdateReason(ctx context.Context, x *domain.Reason) error {
	err := connection(ctx, r.pool).QueryRow(ctx, `UPDATE moderation_reasons SET code = $1, label = $2, description = $3, is_active = $4, updated_at = now()
		WHERE id = $5 RETURNING updated_at`, x.Code, x.Label, x.Description, x.IsActive, x.ID).Scan(&x.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case isUniqueViolation(err):
		return ErrConflict
	}
	return err
}

// CreateReport records a shop's report; a second open report of the same
// review by the same shop is ErrConflict.
func (r *ReviewRepository) CreateReport(ctx context.Context, x *domain.Report) error {
	err := connection(ctx, r.pool).QueryRow(ctx, `INSERT INTO review_reports (review_id, reporting_vendor_id, reason_id, reason_code, reason_label, note)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, status, created_at, updated_at`,
		x.ReviewID, x.ReportingVendorID, x.ReasonID, x.ReasonCode, x.ReasonLabel, x.Note).Scan(&x.ID, &x.Status, &x.CreatedAt, &x.UpdatedAt)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

const reportColumns = `rr.id, rr.review_id, rr.reporting_vendor_id, rr.reason_id, rr.reason_code, rr.reason_label, rr.note, rr.status, rr.decision,
	rr.resolution_reason_id, rr.resolved_by, rr.resolved_at, rr.resolution_note, rr.created_at, rr.updated_at`

func scanReport(row scanner) (*domain.Report, error) {
	var x domain.Report
	err := row.Scan(&x.ID, &x.ReviewID, &x.ReportingVendorID, &x.ReasonID, &x.ReasonCode, &x.ReasonLabel, &x.Note, &x.Status, &x.Decision,
		&x.ResolutionReasonID, &x.ResolvedBy, &x.ResolvedAt, &x.ResolutionNote, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &x, err
}

func (r *ReviewRepository) ListReports(ctx context.Context, status, vendorID, productID string, limit, offset int) ([]*domain.Report, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT `+reportColumns+` FROM review_reports rr JOIN reviews r ON r.id = rr.review_id
		WHERE ($1 = '' OR rr.status = $1) AND ($2 = '' OR r.vendor_id::text = $2) AND ($3 = '' OR r.product_id::text = $3)
		ORDER BY rr.created_at DESC, rr.id LIMIT $4 OFFSET $5`, status, vendorID, productID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*domain.Report, 0)
	for rows.Next() {
		x, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// FindReportForUpdate locks a report for the caller's transaction.
func (r *ReviewRepository) FindReportForUpdate(ctx context.Context, id string) (*domain.Report, error) {
	return scanReport(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+reportColumns+` FROM review_reports rr WHERE rr.id = $1 FOR UPDATE`, id))
}

// ResolveReport closes an open report with the admin's decision.
func (r *ReviewRepository) ResolveReport(ctx context.Context, reportID string, decision domain.ReportDecision, reasonID *string, adminID string, note *string) error {
	tag, err := connection(ctx, r.pool).Exec(ctx, `UPDATE review_reports SET status = 'resolved', decision = $2, resolution_reason_id = $3,
		resolved_by = $4, resolved_at = now(), resolution_note = $5, updated_at = now() WHERE id = $1 AND status = 'open'`,
		reportID, decision, reasonID, adminID, note)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

// Hide takes a published review off the storefront (data kept).
func (r *ReviewRepository) Hide(ctx context.Context, reviewID, reasonID, adminID string, note *string) error {
	tag, err := connection(ctx, r.pool).Exec(ctx, `UPDATE reviews SET status = 'hidden', hidden_reason_id = $2, hidden_by = $3, hidden_note = $4,
		hidden_at = now(), updated_at = now() WHERE id = $1 AND status = 'published'`, reviewID, reasonID, adminID, note)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

// Restore publishes a hidden review again.
func (r *ReviewRepository) Restore(ctx context.Context, reviewID string) error {
	tag, err := connection(ctx, r.pool).Exec(ctx, `UPDATE reviews SET status = 'published', hidden_reason_id = NULL, hidden_by = NULL,
		hidden_note = NULL, hidden_at = NULL, updated_at = now() WHERE id = $1 AND status = 'hidden'`, reviewID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

// AuditEntry is one moderation or shop action.
type AuditEntry struct {
	ActorID, Action, EntityType, EntityID string
	ReviewID, ReportID, ReasonID          *string
	Note                                  *string
	Changes                               map[string]any
}

// Audit appends an entry in the caller's transaction (append-only table).
func (r *ReviewRepository) Audit(ctx context.Context, e AuditEntry) error {
	var changes []byte
	if e.Changes != nil {
		var err error
		if changes, err = json.Marshal(e.Changes); err != nil {
			return fmt.Errorf("encode audit changes: %w", err)
		}
	}
	_, err := connection(ctx, r.pool).Exec(ctx, `INSERT INTO review_moderation_audit_logs (review_id, report_id, reason_id, actor_id, action, note,
		request_id, entity_type, entity_id, changes) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		e.ReviewID, e.ReportID, e.ReasonID, e.ActorID, e.Action, e.Note, middleware.CorrelationID(ctx), e.EntityType, e.EntityID, changes)
	return err
}

// LabelTask is a review whose public author label was never resolved.
type LabelTask struct{ ReviewID, BuyerID string }

func (r *ReviewRepository) PendingLabels(ctx context.Context, limit int) ([]LabelTask, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT id, buyer_id FROM reviews WHERE author_label_checked_at IS NULL ORDER BY created_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LabelTask{}
	for rows.Next() {
		var t LabelTask
		if err := rows.Scan(&t.ReviewID, &t.BuyerID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *ReviewRepository) SetLabel(ctx context.Context, reviewID string, label *string) error {
	_, err := connection(ctx, r.pool).Exec(ctx, `UPDATE reviews SET author_label = $2, author_label_checked_at = now() WHERE id = $1`, reviewID, label)
	return err
}

// Operations is the moderation and upload report (admin dashboard).
func (r *ReviewRepository) Operations(ctx context.Context) (map[string]int64, error) {
	var open, oldest, hidden7, created24, pending, parked, labels int64
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT
		(SELECT count(*) FROM review_reports WHERE status = 'open'),
		(SELECT COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)) / 3600, 0)::bigint FROM review_reports WHERE status = 'open'),
		(SELECT count(*) FROM reviews WHERE status = 'hidden' AND hidden_at > now() - interval '7 days'),
		(SELECT count(*) FROM reviews WHERE created_at > now() - interval '24 hours'),
		(SELECT count(*) FROM review_image_uploads WHERE parked_at IS NULL AND created_at < now() - interval '1 hour'),
		(SELECT count(*) FROM review_image_uploads WHERE parked_at IS NOT NULL),
		(SELECT count(*) FROM reviews WHERE author_label_checked_at IS NULL)`).
		Scan(&open, &oldest, &hidden7, &created24, &pending, &parked, &labels)
	return map[string]int64{
		"open_reports": open, "oldest_open_report_hours": oldest, "hidden_7d": hidden7, "reviews_24h": created24,
		"image_cleanup_pending": pending, "image_cleanup_parked": parked, "author_labels_pending": labels,
	}, err
}
