package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"shopee/backend/pkg/middleware"
	"shopee/backend/services/review/internal/domain"
	"shopee/backend/services/review/internal/repository/reviewdb"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("review repository: not found")
var ErrConflict = errors.New("review repository: conflict")

// ReviewRepository runs the queries in queries/*.sql (code generated into
// reviewdb by sqlc, see sqlc.yaml) and maps their rows to domain types.
type ReviewRepository struct{ pool *pgxpool.Pool }

func NewReviewRepository(pool *pgxpool.Pool) *ReviewRepository { return &ReviewRepository{pool: pool} }

// db runs on the caller's transaction when ctx carries one.
func (r *ReviewRepository) db(ctx context.Context) *reviewdb.Queries {
	return reviewdb.New(connection(ctx, r.pool))
}

func toReview(x reviewdb.Review) *domain.Review {
	return &domain.Review{ID: x.ID, BuyerID: x.BuyerID, VendorID: x.VendorID, ProductID: x.ProductID, OrderItemID: x.OrderItemID,
		VendorOrderID: x.VendorOrderID, Rating: x.Rating, Comment: x.Comment, Status: domain.ReviewStatus(x.Status),
		VerifiedPurchase: x.VerifiedPurchase, AuthorLabel: x.AuthorLabel, HiddenReasonID: x.HiddenReasonID, HiddenBy: x.HiddenBy,
		HiddenNote: x.HiddenNote, HiddenAt: x.HiddenAt, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}

func toReport(x reviewdb.ReviewReport) *domain.Report {
	return &domain.Report{ID: x.ID, ReviewID: x.ReviewID, ReportingVendorID: x.ReportingVendorID, ReasonID: x.ReasonID,
		ReasonCode: x.ReasonCode, ReasonLabel: x.ReasonLabel, Note: x.Note, Status: domain.ReportStatus(x.Status),
		Decision: (*domain.ReportDecision)(x.Decision), ResolutionReasonID: x.ResolutionReasonID, ResolvedBy: x.ResolvedBy,
		ResolvedAt: x.ResolvedAt, ResolutionNote: x.ResolutionNote, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}

// Rows whose columns are exactly a domain struct's fields convert directly;
// the compiler rejects the conversion as soon as the two drift apart.
func toImage(x reviewdb.ReviewImage) *domain.Image {
	i := domain.Image(x)
	return &i
}

func toReason(x reviewdb.ModerationReason) *domain.Reason {
	v := domain.Reason(x)
	return &v
}

func toUpload(x reviewdb.ClaimStaleUploadsRow) Upload   { return Upload(x) }
func toLabelTask(x reviewdb.PendingLabelsRow) LabelTask { return LabelTask(x) }

func toSummary(x reviewdb.ProductSummaryRow) domain.Summary {
	return domain.Summary{RatingAverage: x.RatingAverage, RatingCount: x.RatingCount,
		Distribution: [5]int64{x.Rated1, x.Rated2, x.Rated3, x.Rated4, x.Rated5}}
}

// one maps a single-row result, turning "no row" into ErrNotFound.
func one[T, U any](row T, err error, to func(T) U) (U, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		var zero U
		return zero, ErrNotFound
	}
	if err != nil {
		var zero U
		return zero, err
	}
	return to(row), nil
}

// all maps a list result; an empty list is never nil.
func all[T, U any](rows []T, err error, to func(T) U) ([]U, error) {
	if err != nil {
		return nil, err
	}
	out := make([]U, 0, len(rows))
	for _, row := range rows {
		out = append(out, to(row))
	}
	return out, nil
}

// changedOne turns a guarded update's row count into ErrConflict when the
// row was not in the expected state.
func changedOne(n int64, err error) error {
	if err == nil && n != 1 {
		return ErrConflict
	}
	return err
}

// Create records a review; labelChecked says whether Identity answered
// (the label may still be nil for an unknown user). A second review of the
// same order item is ErrConflict, also under concurrent requests.
func (r *ReviewRepository) Create(ctx context.Context, v *domain.Review, labelChecked bool) error {
	row, err := r.db(ctx).CreateReview(ctx, reviewdb.CreateReviewParams{BuyerID: v.BuyerID, VendorID: v.VendorID, ProductID: v.ProductID,
		OrderItemID: v.OrderItemID, VendorOrderID: v.VendorOrderID, Rating: v.Rating, Comment: v.Comment,
		VerifiedPurchase: v.VerifiedPurchase, AuthorLabel: v.AuthorLabel, LabelChecked: labelChecked})
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	*v = *toReview(row)
	return nil
}

func (r *ReviewRepository) Find(ctx context.Context, id string) (*domain.Review, error) {
	row, err := r.db(ctx).GetReview(ctx, id)
	return one(row, err, toReview)
}

// FindForUpdate locks the review for the caller's transaction.
func (r *ReviewRepository) FindForUpdate(ctx context.Context, id string) (*domain.Review, error) {
	row, err := r.db(ctx).GetReviewForUpdate(ctx, id)
	return one(row, err, toReview)
}

// ReviewedItems is the set of order items the buyer already reviewed.
func (r *ReviewRepository) ReviewedItems(ctx context.Context, buyerID string) (map[string]bool, error) {
	ids, err := r.db(ctx).ReviewedItemIDs(ctx, buyerID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// ListPublic is the storefront list: published reviews, and only verified
// purchases unless includeUnverified (local/staging demo data).
func (r *ReviewRepository) ListPublic(ctx context.Context, productID string, rating int, includeUnverified bool, limit, offset int) ([]*domain.Review, error) {
	rows, err := r.db(ctx).ListPublicReviews(ctx, reviewdb.ListPublicReviewsParams{ProductID: productID, IncludeUnverified: includeUnverified,
		Rating: rating, PageLimit: limit, PageOffset: offset})
	return all(rows, err, toReview)
}

func (r *ReviewRepository) ListBuyer(ctx context.Context, buyerID string, limit, offset int) ([]*domain.Review, error) {
	rows, err := r.db(ctx).ListBuyerReviews(ctx, reviewdb.ListBuyerReviewsParams{BuyerID: buyerID, PageLimit: limit, PageOffset: offset})
	return all(rows, err, toReview)
}

func (r *ReviewRepository) ListVendor(ctx context.Context, vendorID, productID string, rating int, replied *bool, includeUnverified bool, limit, offset int) ([]*domain.Review, error) {
	rows, err := r.db(ctx).ListVendorReviews(ctx, reviewdb.ListVendorReviewsParams{VendorID: vendorID, ProductID: productID, Rating: rating,
		Replied: replied, IncludeUnverified: includeUnverified, PageLimit: limit, PageOffset: offset})
	return all(rows, err, toReview)
}

// AdminFilter narrows the moderation list.
type AdminFilter struct {
	BuyerID, VendorID, ProductID, Status string
	Rating                               int
}

func (r *ReviewRepository) ListAdmin(ctx context.Context, f AdminFilter, limit, offset int) ([]*domain.Review, error) {
	rows, err := r.db(ctx).ListAdminReviews(ctx, reviewdb.ListAdminReviewsParams{BuyerID: f.BuyerID, VendorID: f.VendorID,
		ProductID: f.ProductID, Status: f.Status, Rating: f.Rating, PageLimit: limit, PageOffset: offset})
	return all(rows, err, toReview)
}

// Summary counts exactly what the public list can show.
func (r *ReviewRepository) Summary(ctx context.Context, productID string, includeUnverified bool) (domain.Summary, error) {
	row, err := r.db(ctx).ProductSummary(ctx, reviewdb.ProductSummaryParams{ProductID: productID, IncludeUnverified: includeUnverified})
	return toSummary(row), err
}

func (r *ReviewRepository) VendorSummary(ctx context.Context, vendorID string, includeUnverified bool) (domain.Summary, error) {
	row, err := r.db(ctx).VendorSummary(ctx, reviewdb.VendorSummaryParams{VendorID: vendorID, IncludeUnverified: includeUnverified})
	return toSummary(reviewdb.ProductSummaryRow(row)), err
}

// ImagesFor returns the images of several reviews in one query.
func (r *ReviewRepository) ImagesFor(ctx context.Context, reviewIDs []string) (map[string][]*domain.Image, error) {
	out := map[string][]*domain.Image{}
	if len(reviewIDs) == 0 {
		return out, nil
	}
	rows, err := r.db(ctx).ImagesForReviews(ctx, reviewIDs)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ReviewID] = append(out[row.ReviewID], toImage(row))
	}
	return out, nil
}

// RepliesFor returns the shop replies of several reviews in one query.
func (r *ReviewRepository) RepliesFor(ctx context.Context, reviewIDs []string) (map[string]*domain.Reply, error) {
	out := map[string]*domain.Reply{}
	if len(reviewIDs) == 0 {
		return out, nil
	}
	rows, err := r.db(ctx).RepliesForReviews(ctx, reviewIDs)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		reply := domain.Reply(row)
		out[row.ReviewID] = &reply
	}
	return out, nil
}

// SetReply writes the shop's reply and returns the previous message (nil
// for a first reply), in the caller's transaction.
func (r *ReviewRepository) SetReply(ctx context.Context, reply *domain.Reply) (*string, error) {
	q := r.db(ctx)
	var previous *string
	message, err := q.LockReplyMessage(ctx, reply.ReviewID)
	switch {
	case err == nil:
		previous = &message
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	row, err := q.UpsertReply(ctx, reviewdb.UpsertReplyParams{ReviewID: reply.ReviewID, VendorID: reply.VendorID, Message: reply.Message})
	if err != nil {
		return nil, err
	}
	reply.CreatedAt, reply.UpdatedAt = row.CreatedAt, row.UpdatedAt
	return previous, nil
}

// CountImageSlots counts stored images plus uploads in progress of a
// review (lock the review first so concurrent uploads queue up).
func (r *ReviewRepository) CountImageSlots(ctx context.Context, reviewID string) (int, error) {
	n, err := r.db(ctx).CountImageSlots(ctx, reviewID)
	return int(n), err
}

// AddUpload records that objectKey is about to be stored for reviewID.
func (r *ReviewRepository) AddUpload(ctx context.Context, reviewID, objectKey string) error {
	return r.db(ctx).AddUpload(ctx, reviewdb.AddUploadParams{ReviewID: reviewID, ObjectKey: objectKey})
}

// RecordImage stores the image row at the next position and closes its
// upload, in the caller's transaction (review locked).
func (r *ReviewRepository) RecordImage(ctx context.Context, image *domain.Image) error {
	q := r.db(ctx)
	row, err := q.InsertImage(ctx, reviewdb.InsertImageParams{ReviewID: image.ReviewID, ObjectKey: image.ObjectKey, URL: image.URL,
		ContentType: image.ContentType, SizeBytes: image.SizeBytes})
	if err != nil {
		return err
	}
	image.ID, image.Position, image.CreatedAt = row.ID, row.Position, row.CreatedAt
	return q.DeleteUpload(ctx, image.ObjectKey)
}

// DeleteUpload closes an upload whose object is gone (or was never stored).
func (r *ReviewRepository) DeleteUpload(ctx context.Context, objectKey string) error {
	return r.db(ctx).DeleteUpload(ctx, objectKey)
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
	rows, err := r.db(ctx).ClaimStaleUploads(ctx, limit)
	return all(rows, err, toUpload)
}

// UploadCleanupFailed schedules another try, or parks the upload.
func (r *ReviewRepository) UploadCleanupFailed(ctx context.Context, objectKey, reason string, next time.Time, park bool) error {
	if len(reason) > 300 {
		reason = reason[:300]
	}
	return r.db(ctx).UploadCleanupFailed(ctx, reviewdb.UploadCleanupFailedParams{ObjectKey: objectKey, LastError: reason,
		NextAttemptAt: next, Park: park})
}

func (r *ReviewRepository) ListReasons(ctx context.Context, activeOnly bool) ([]*domain.Reason, error) {
	rows, err := r.db(ctx).ListReasons(ctx, activeOnly)
	return all(rows, err, toReason)
}

func (r *ReviewRepository) FindReason(ctx context.Context, id string) (*domain.Reason, error) {
	row, err := r.db(ctx).GetReason(ctx, id)
	return one(row, err, toReason)
}

func (r *ReviewRepository) CreateReason(ctx context.Context, x *domain.Reason) error {
	row, err := r.db(ctx).CreateReason(ctx, reviewdb.CreateReasonParams{Code: x.Code, Label: x.Label, Description: x.Description})
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	x.ID, x.IsActive, x.CreatedAt, x.UpdatedAt = row.ID, row.IsActive, row.CreatedAt, row.UpdatedAt
	return nil
}

func (r *ReviewRepository) UpdateReason(ctx context.Context, x *domain.Reason) error {
	updatedAt, err := r.db(ctx).UpdateReason(ctx, reviewdb.UpdateReasonParams{ID: x.ID, Code: x.Code, Label: x.Label,
		Description: x.Description, IsActive: x.IsActive})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case isUniqueViolation(err):
		return ErrConflict
	case err == nil:
		x.UpdatedAt = updatedAt
	}
	return err
}

// CreateReport records a shop's report; a second open report of the same
// review by the same shop is ErrConflict.
func (r *ReviewRepository) CreateReport(ctx context.Context, x *domain.Report) error {
	row, err := r.db(ctx).CreateReport(ctx, reviewdb.CreateReportParams{ReviewID: x.ReviewID, ReportingVendorID: x.ReportingVendorID,
		ReasonID: x.ReasonID, ReasonCode: x.ReasonCode, ReasonLabel: x.ReasonLabel, Note: x.Note})
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	x.ID, x.Status, x.CreatedAt, x.UpdatedAt = row.ID, domain.ReportStatus(row.Status), row.CreatedAt, row.UpdatedAt
	return nil
}

func (r *ReviewRepository) ListReports(ctx context.Context, status, vendorID, productID string, limit, offset int) ([]*domain.Report, error) {
	rows, err := r.db(ctx).ListReports(ctx, reviewdb.ListReportsParams{Status: status, VendorID: vendorID, ProductID: productID,
		PageLimit: limit, PageOffset: offset})
	return all(rows, err, toReport)
}

// FindReportForUpdate locks a report for the caller's transaction.
func (r *ReviewRepository) FindReportForUpdate(ctx context.Context, id string) (*domain.Report, error) {
	row, err := r.db(ctx).GetReportForUpdate(ctx, id)
	return one(row, err, toReport)
}

// ResolveReport closes an open report with the admin's decision.
func (r *ReviewRepository) ResolveReport(ctx context.Context, reportID string, decision domain.ReportDecision, reasonID *string, adminID string, note *string) error {
	return changedOne(r.db(ctx).ResolveReport(ctx, reviewdb.ResolveReportParams{ID: reportID, Decision: string(decision),
		ResolutionReasonID: reasonID, ResolvedBy: adminID, ResolutionNote: note}))
}

// Hide takes a published review off the storefront (data kept).
func (r *ReviewRepository) Hide(ctx context.Context, reviewID, reasonID, adminID string, note *string) error {
	return changedOne(r.db(ctx).HideReview(ctx, reviewdb.HideReviewParams{ID: reviewID, HiddenReasonID: reasonID, HiddenBy: adminID,
		HiddenNote: note}))
}

// Restore publishes a hidden review again.
func (r *ReviewRepository) Restore(ctx context.Context, reviewID string) error {
	return changedOne(r.db(ctx).RestoreReview(ctx, reviewID))
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
	return r.db(ctx).InsertAudit(ctx, reviewdb.InsertAuditParams{ReviewID: e.ReviewID, ReportID: e.ReportID, ReasonID: e.ReasonID,
		ActorID: e.ActorID, Action: e.Action, Note: e.Note, RequestID: middleware.CorrelationID(ctx), EntityType: e.EntityType,
		EntityID: e.EntityID, Changes: changes})
}

// LabelTask is a review whose public author label was never resolved.
type LabelTask struct{ ReviewID, BuyerID string }

func (r *ReviewRepository) PendingLabels(ctx context.Context, limit int) ([]LabelTask, error) {
	rows, err := r.db(ctx).PendingLabels(ctx, limit)
	return all(rows, err, toLabelTask)
}

func (r *ReviewRepository) SetLabel(ctx context.Context, reviewID string, label *string) error {
	return r.db(ctx).SetLabel(ctx, reviewdb.SetLabelParams{ID: reviewID, AuthorLabel: label})
}

// Operations is the moderation and upload report (admin dashboard).
func (r *ReviewRepository) Operations(ctx context.Context) (map[string]int64, error) {
	x, err := r.db(ctx).Operations(ctx)
	return map[string]int64{
		"open_reports": x.OpenReports, "oldest_open_report_hours": x.OldestOpenReportHours, "hidden_7d": x.Hidden7d,
		"reviews_24h": x.Reviews24h, "image_cleanup_pending": x.ImageCleanupPending, "image_cleanup_parked": x.ImageCleanupParked,
		"author_labels_pending": x.AuthorLabelsPending,
	}, err
}
