package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"shopee/backend/pkg/middleware"
	"shopee/backend/services/review/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("review repository: not found")
var ErrConflict = errors.New("review repository: conflict")

type ReviewRepository struct{ pool *pgxpool.Pool }

func NewReviewRepository(pool *pgxpool.Pool) *ReviewRepository { return &ReviewRepository{pool: pool} }

const reviewColumns = `id, buyer_id, vendor_id, product_id, order_item_id, vendor_order_id, rating, comment, status, hidden_reason_id, hidden_by, hidden_at, created_at, updated_at`

type scanner interface{ Scan(...any) error }

func scanReview(row scanner) (*domain.Review, error) {
	var v domain.Review
	err := row.Scan(&v.ID, &v.BuyerID, &v.VendorID, &v.ProductID, &v.OrderItemID, &v.VendorOrderID, &v.Rating, &v.Comment, &v.Status, &v.HiddenReasonID, &v.HiddenBy, &v.HiddenAt, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}
func (r *ReviewRepository) Create(ctx context.Context, v *domain.Review) error {
	err := r.pool.QueryRow(ctx, `INSERT INTO reviews (buyer_id,vendor_id,product_id,order_item_id,vendor_order_id,rating,comment) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+reviewColumns, v.BuyerID, v.VendorID, v.ProductID, v.OrderItemID, v.VendorOrderID, v.Rating, v.Comment).Scan(&v.ID, &v.BuyerID, &v.VendorID, &v.ProductID, &v.OrderItemID, &v.VendorOrderID, &v.Rating, &v.Comment, &v.Status, &v.HiddenReasonID, &v.HiddenBy, &v.HiddenAt, &v.CreatedAt, &v.UpdatedAt)
	if err != nil && strings.Contains(err.Error(), "reviews_buyer_id_order_item_id_key") {
		return ErrConflict
	}
	return err
}
func (r *ReviewRepository) Find(ctx context.Context, id string) (*domain.Review, error) {
	return scanReview(r.pool.QueryRow(ctx, `SELECT `+reviewColumns+` FROM reviews WHERE id=$1`, id))
}
func (r *ReviewRepository) ListPublic(ctx context.Context, productID string, rating, limit, offset int) ([]*domain.Review, error) {
	return r.list(ctx, `SELECT `+reviewColumns+` FROM reviews WHERE product_id=$1 AND status='published' AND ($2=0 OR rating=$2) ORDER BY created_at DESC LIMIT $3 OFFSET $4`, productID, rating, limit, offset)
}
func (r *ReviewRepository) ListBuyer(ctx context.Context, buyerID string, limit, offset int) ([]*domain.Review, error) {
	return r.list(ctx, `SELECT `+reviewColumns+` FROM reviews WHERE buyer_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, buyerID, limit, offset)
}
func (r *ReviewRepository) ListVendor(ctx context.Context, vendorID, productID string, rating int, replied *bool, limit, offset int) ([]*domain.Review, error) {
	repliedValue := -1
	if replied != nil {
		if *replied {
			repliedValue = 1
		} else {
			repliedValue = 0
		}
	}
	return r.list(ctx, `SELECT `+reviewColumns+` FROM reviews r WHERE r.vendor_id=$1 AND r.status='published' AND ($2='' OR r.product_id=NULLIF($2,'')::uuid) AND ($3=0 OR r.rating=$3) AND ($4=-1 OR ($4=1 AND EXISTS (SELECT 1 FROM review_replies rr WHERE rr.review_id=r.id)) OR ($4=0 AND NOT EXISTS (SELECT 1 FROM review_replies rr WHERE rr.review_id=r.id))) ORDER BY r.created_at DESC LIMIT $5 OFFSET $6`, vendorID, productID, rating, repliedValue, limit, offset)
}
func (r *ReviewRepository) ListAdmin(ctx context.Context, buyerID, vendorID, productID, status string, rating, limit, offset int) ([]*domain.Review, error) {
	return r.list(ctx, `SELECT `+reviewColumns+` FROM reviews WHERE ($1='' OR buyer_id::text=$1) AND ($2='' OR vendor_id::text=$2) AND ($3='' OR product_id::text=$3) AND ($4='' OR status=$4) AND ($5=0 OR rating=$5) ORDER BY created_at DESC LIMIT $6 OFFSET $7`, buyerID, vendorID, productID, status, rating, limit, offset)
}
func (r *ReviewRepository) list(ctx context.Context, q string, args ...any) ([]*domain.Review, error) {
	rows, err := r.pool.Query(ctx, q, args...)
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
func (r *ReviewRepository) Summary(ctx context.Context, productID string) (domain.Summary, error) {
	var s domain.Summary
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(avg(rating),0),count(*),count(*) FILTER (WHERE rating=1),count(*) FILTER (WHERE rating=2),count(*) FILTER (WHERE rating=3),count(*) FILTER (WHERE rating=4),count(*) FILTER (WHERE rating=5) FROM reviews WHERE product_id=$1 AND status='published'`, productID).Scan(&s.RatingAverage, &s.RatingCount, &s.Distribution[0], &s.Distribution[1], &s.Distribution[2], &s.Distribution[3], &s.Distribution[4])
	return s, err
}
func (r *ReviewRepository) VendorSummary(ctx context.Context, vendorID string) (domain.Summary, error) {
	var s domain.Summary
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(avg(rating),0),count(*),count(*) FILTER (WHERE rating=1),count(*) FILTER (WHERE rating=2),count(*) FILTER (WHERE rating=3),count(*) FILTER (WHERE rating=4),count(*) FILTER (WHERE rating=5) FROM reviews WHERE vendor_id=$1 AND status='published'`, vendorID).Scan(&s.RatingAverage, &s.RatingCount, &s.Distribution[0], &s.Distribution[1], &s.Distribution[2], &s.Distribution[3], &s.Distribution[4])
	return s, err
}

func (r *ReviewRepository) CreateImage(ctx context.Context, image *domain.Image) error {
	return r.pool.QueryRow(ctx, `INSERT INTO review_images(review_id,object_key,url,content_type,size_bytes,position) VALUES($1,$2,$3,$4,$5,$6) RETURNING id,created_at`, image.ReviewID, image.ObjectKey, image.URL, image.ContentType, image.SizeBytes, image.Position).Scan(&image.ID, &image.CreatedAt)
}
func (r *ReviewRepository) CountImages(ctx context.Context, reviewID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM review_images WHERE review_id=$1`, reviewID).Scan(&n)
	return n, err
}
func (r *ReviewRepository) ListImages(ctx context.Context, reviewID string) ([]*domain.Image, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,review_id,object_key,url,content_type,size_bytes,position,created_at FROM review_images WHERE review_id=$1 ORDER BY position`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*domain.Image, 0)
	for rows.Next() {
		var i domain.Image
		if err := rows.Scan(&i.ID, &i.ReviewID, &i.ObjectKey, &i.URL, &i.ContentType, &i.SizeBytes, &i.Position, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &i)
	}
	return out, rows.Err()
}
func (r *ReviewRepository) GetReply(ctx context.Context, reviewID string) (*domain.Reply, error) {
	var x domain.Reply
	err := r.pool.QueryRow(ctx, `SELECT review_id,vendor_id,message,created_at,updated_at FROM review_replies WHERE review_id=$1`, reviewID).Scan(&x.ReviewID, &x.VendorID, &x.Message, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &x, nil
}
func (r *ReviewRepository) UpsertReply(ctx context.Context, reply *domain.Reply) error {
	return r.pool.QueryRow(ctx, `INSERT INTO review_replies(review_id,vendor_id,message) VALUES($1,$2,$3) ON CONFLICT(review_id) DO UPDATE SET message=excluded.message,updated_at=now() RETURNING created_at,updated_at`, reply.ReviewID, reply.VendorID, reply.Message).Scan(&reply.CreatedAt, &reply.UpdatedAt)
}
func (r *ReviewRepository) ListReasons(ctx context.Context, activeOnly bool) ([]*domain.Reason, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,code,label,description,is_active,created_at,updated_at FROM moderation_reasons WHERE ($1=false OR is_active=true) ORDER BY label`, activeOnly)
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
	err := r.pool.QueryRow(ctx, `SELECT id,code,label,description,is_active,created_at,updated_at FROM moderation_reasons WHERE id=$1`, id).Scan(&x.ID, &x.Code, &x.Label, &x.Description, &x.IsActive, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &x, nil
}
func (r *ReviewRepository) CreateReason(ctx context.Context, x *domain.Reason) error {
	return r.pool.QueryRow(ctx, `INSERT INTO moderation_reasons(code,label,description) VALUES($1,$2,$3) RETURNING id,is_active,created_at,updated_at`, x.Code, x.Label, x.Description).Scan(&x.ID, &x.IsActive, &x.CreatedAt, &x.UpdatedAt)
}
func (r *ReviewRepository) UpdateReason(ctx context.Context, x *domain.Reason) error {
	tag, err := r.pool.Exec(ctx, `UPDATE moderation_reasons SET code=$1,label=$2,description=$3,is_active=$4,updated_at=now() WHERE id=$5`, x.Code, x.Label, x.Description, x.IsActive, x.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
func (r *ReviewRepository) CreateReport(ctx context.Context, x *domain.Report) error {
	err := r.pool.QueryRow(ctx, `INSERT INTO review_reports(review_id,reporting_vendor_id,reason_id,reason_code,reason_label,note) VALUES($1,$2,$3,$4,$5,$6) RETURNING id,status,created_at,updated_at`, x.ReviewID, x.ReportingVendorID, x.ReasonID, x.ReasonCode, x.ReasonLabel, x.Note).Scan(&x.ID, &x.Status, &x.CreatedAt, &x.UpdatedAt)
	if err != nil && strings.Contains(err.Error(), "review_reports_open_vendor_idx") {
		return ErrConflict
	}
	return err
}
func (r *ReviewRepository) ListReports(ctx context.Context, status, vendorID, productID string, limit, offset int) ([]*domain.Report, error) {
	q := `SELECT rr.id,rr.review_id,rr.reporting_vendor_id,rr.reason_id,rr.reason_code,rr.reason_label,rr.note,rr.status,rr.decision,rr.resolution_reason_id,rr.resolved_by,rr.resolved_at,rr.resolution_note,rr.created_at,rr.updated_at FROM review_reports rr JOIN reviews r ON r.id=rr.review_id WHERE ($1='' OR rr.status=$1) AND ($2='' OR r.vendor_id::text=$2) AND ($3='' OR r.product_id::text=$3) ORDER BY rr.created_at DESC LIMIT $4 OFFSET $5`
	rows, err := r.pool.Query(ctx, q, status, vendorID, productID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*domain.Report, 0)
	for rows.Next() {
		var x domain.Report
		if err := rows.Scan(&x.ID, &x.ReviewID, &x.ReportingVendorID, &x.ReasonID, &x.ReasonCode, &x.ReasonLabel, &x.Note, &x.Status, &x.Decision, &x.ResolutionReasonID, &x.ResolvedBy, &x.ResolvedAt, &x.ResolutionNote, &x.CreatedAt, &x.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &x)
	}
	return out, rows.Err()
}
func (r *ReviewRepository) ResolveReport(ctx context.Context, reportID string, decision domain.ReportDecision, reasonID, adminID string, note *string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var reviewID string
	var current domain.ReportStatus
	if err := tx.QueryRow(ctx, `SELECT review_id,status FROM review_reports WHERE id=$1 FOR UPDATE`, reportID).Scan(&reviewID, &current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if current != domain.ReportOpen {
		return ErrConflict
	}
	var resolutionReasonID any
	if reasonID != "" {
		resolutionReasonID = reasonID
	}
	tag, err := tx.Exec(ctx, `UPDATE review_reports SET status='resolved',decision=$1,resolution_reason_id=$2,resolved_by=$3,resolved_at=now(),resolution_note=$4,updated_at=now() WHERE id=$5`, decision, resolutionReasonID, adminID, note, reportID)
	if err != nil || tag.RowsAffected() != 1 {
		if err != nil {
			return err
		}
		return ErrNotFound
	}
	if decision == domain.DecisionHide {
		if _, err := tx.Exec(ctx, `UPDATE reviews SET status='hidden',hidden_reason_id=$1,hidden_by=$2,hidden_at=now(),updated_at=now() WHERE id=$3`, reasonID, adminID, reviewID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO review_moderation_audit_logs(review_id,report_id,reason_id,actor_id,action,note,request_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, reviewID, reportID, resolutionReasonID, adminID, fmt.Sprintf("report_%s", decision), note, middleware.CorrelationID(ctx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
