// Package usecase runs Review's workflows: a buyer reviews an item Order
// confirms was received in a completed order; the storefront shows
// published reviews with a masked author label; the shop owning the review
// replies or reports it; an admin keeps, hides or restores it. Every shop
// and moderation action is audited in its transaction. Review never
// changes an order or decides a refund, and a later return or refund does
// not remove a review.
package usecase

import (
	"context"
	"errors"
	"path"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/review/internal/adapter"
	"shopee/backend/services/review/internal/domain"
	"shopee/backend/services/review/internal/repository"
)

type ReviewRepository interface {
	Create(ctx context.Context, v *domain.Review, labelChecked bool) error
	Find(ctx context.Context, id string) (*domain.Review, error)
	FindForUpdate(ctx context.Context, id string) (*domain.Review, error)
	ReviewedItems(ctx context.Context, buyerID string) (map[string]bool, error)
	ListPublic(ctx context.Context, productID string, rating int, includeUnverified bool, limit, offset int) ([]*domain.Review, error)
	ListBuyer(ctx context.Context, buyerID string, limit, offset int) ([]*domain.Review, error)
	ListVendor(ctx context.Context, vendorID, productID string, rating int, replied *bool, includeUnverified bool, limit, offset int) ([]*domain.Review, error)
	ListAdmin(ctx context.Context, f repository.AdminFilter, limit, offset int) ([]*domain.Review, error)
	Summary(ctx context.Context, productID string, includeUnverified bool) (domain.Summary, error)
	VendorSummary(ctx context.Context, vendorID string, includeUnverified bool) (domain.Summary, error)
	ImagesFor(ctx context.Context, reviewIDs []string) (map[string][]*domain.Image, error)
	RepliesFor(ctx context.Context, reviewIDs []string) (map[string]*domain.Reply, error)
	SetReply(ctx context.Context, reply *domain.Reply) (*string, error)
	CountImageSlots(ctx context.Context, reviewID string) (int, error)
	AddUpload(ctx context.Context, reviewID, objectKey string) error
	RecordImage(ctx context.Context, image *domain.Image) error
	DeleteUpload(ctx context.Context, objectKey string) error
	ClaimStaleUploads(ctx context.Context, limit int) ([]repository.Upload, error)
	UploadCleanupFailed(ctx context.Context, objectKey, reason string, next time.Time, park bool) error
	ListReasons(ctx context.Context, activeOnly bool) ([]*domain.Reason, error)
	FindReason(ctx context.Context, id string) (*domain.Reason, error)
	CreateReason(ctx context.Context, x *domain.Reason) error
	UpdateReason(ctx context.Context, x *domain.Reason) error
	CreateReport(ctx context.Context, x *domain.Report) error
	ListReports(ctx context.Context, status, vendorID, productID string, limit, offset int) ([]*domain.Report, error)
	FindReportForUpdate(ctx context.Context, id string) (*domain.Report, error)
	ResolveReport(ctx context.Context, reportID string, decision domain.ReportDecision, reasonID *string, adminID string, note *string) error
	Hide(ctx context.Context, reviewID, reasonID, adminID string, note *string) error
	Restore(ctx context.Context, reviewID string) error
	Audit(ctx context.Context, e repository.AuditEntry) error
	PendingLabels(ctx context.Context, limit int) ([]repository.LabelTask, error)
	SetLabel(ctx context.Context, reviewID string, label *string) error
	Operations(ctx context.Context) (map[string]int64, error)
}

type ObjectStore interface {
	Upload(ctx context.Context, key string, data []byte, contentType string) (string, error)
	Delete(ctx context.Context, key string) error
}

// RoleVerifier re-verifies an admin with Identity (identityclient.Client).
type RoleVerifier interface {
	RequireRole(ctx context.Context, userID, role string) error
}

// Transactor runs fn in one database transaction.
type Transactor interface {
	Run(ctx context.Context, fn func(context.Context) error) error
}

type Deps struct {
	Repo     ReviewRepository
	Tx       Transactor
	Orders   adapter.OrderGateway
	Vendors  adapter.VendorGateway
	Identity adapter.IdentityGateway
	Store    ObjectStore
	Roles    RoleVerifier
	Log      zerolog.Logger
	// ShowUnverified also lists reviews that are not verified purchases
	// (seeded demo data); never in production.
	ShowUnverified bool
	Now            func() time.Time
}

type ReviewUseCase struct {
	Deps
	// imageSlots bounds how many photos are decoded at once (memory).
	imageSlots chan struct{}
}

func NewReviewUseCase(d Deps) *ReviewUseCase {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &ReviewUseCase{Deps: d, imageSlots: make(chan struct{}, 2)}
}

func failure(err error) error {
	var app *apperror.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &app):
		return app
	case errors.Is(err, repository.ErrNotFound):
		return apperror.NotFound("Review resource not found")
	case errors.Is(err, repository.ErrConflict):
		return apperror.Conflict("This action has already been completed")
	}
	return apperror.Internal(err)
}

func validID(id, what string) error {
	if _, err := uuid.Parse(id); err != nil {
		return apperror.Validation("Invalid " + what)
	}
	return nil
}

func (u *ReviewUseCase) requireAdmin(ctx context.Context, adminID string) error {
	if u.Roles == nil {
		return apperror.Internal(errors.New("admin verification is not configured"))
	}
	if err := u.Roles.RequireRole(ctx, adminID, "admin"); err != nil {
		return failure(err)
	}
	return nil
}

func (u *ReviewUseCase) inTx(ctx context.Context, fn func(context.Context) error) error {
	if u.Tx == nil {
		return apperror.Internal(errors.New("transactions are not configured"))
	}
	return failure(u.Tx.Run(ctx, fn))
}

// eligible asks Order which items the buyer received in completed orders.
func (u *ReviewUseCase) eligible(ctx context.Context, buyerID, productID string) ([]adapter.EligibleOrderItem, error) {
	items, err := u.Orders.ListEligible(ctx, buyerID, productID)
	if err != nil {
		u.Log.Warn().Err(err).Msg("review_eligibility_unavailable")
		return nil, failure(err)
	}
	return items, nil
}

// Create records a verified-purchase review of an order item Order
// confirms, once per item (also under concurrent requests).
func (u *ReviewUseCase) Create(ctx context.Context, buyerID, orderItemID string, rating int, comment string) (*domain.Review, error) {
	comment, err := domain.ValidateReview(rating, comment)
	if err != nil {
		return nil, err
	}
	if err := validID(orderItemID, "order item"); err != nil {
		return nil, err
	}
	items, err := u.eligible(ctx, buyerID, "")
	if err != nil {
		return nil, err
	}
	var item *adapter.EligibleOrderItem
	for i := range items {
		if items[i].OrderItemID == orderItemID {
			item = &items[i]
			break
		}
	}
	if item == nil {
		return nil, apperror.Forbidden("You can review only products from your completed orders")
	}
	label, checked := u.authorLabel(ctx, buyerID)
	v := &domain.Review{BuyerID: buyerID, OrderItemID: item.OrderItemID, VendorOrderID: item.VendorOrderID, ProductID: item.ProductID,
		VendorID: item.VendorID, Rating: rating, Comment: comment, VerifiedPurchase: true, AuthorLabel: label}
	if err := u.Repo.Create(ctx, v, checked); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return nil, apperror.Conflict("You have already reviewed this item")
		}
		return nil, failure(err)
	}
	u.Log.Info().Str("review_id", v.ID).Str("product_id", v.ProductID).Int("rating", v.Rating).Msg("review_created")
	return v, nil
}

// authorLabel resolves the masked public name; checked is false when
// Identity did not answer (the backfill tries again later).
func (u *ReviewUseCase) authorLabel(ctx context.Context, buyerID string) (*string, bool) {
	if u.Identity == nil {
		return nil, false
	}
	lctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	name, err := u.Identity.DisplayName(lctx, buyerID)
	if err != nil {
		return nil, false
	}
	if masked := domain.MaskName(name); masked != "" {
		return &masked, true
	}
	return nil, true
}

// Eligibility lists the buyer's received items not reviewed yet.
func (u *ReviewUseCase) Eligibility(ctx context.Context, buyerID, productID string) ([]adapter.EligibleOrderItem, error) {
	if productID != "" {
		if err := validID(productID, "product"); err != nil {
			return nil, err
		}
	}
	items, err := u.eligible(ctx, buyerID, productID)
	if err != nil {
		return nil, err
	}
	reviewed, err := u.Repo.ReviewedItems(ctx, buyerID)
	if err != nil {
		return nil, failure(err)
	}
	out := make([]adapter.EligibleOrderItem, 0, len(items))
	for _, item := range items {
		if !reviewed[item.OrderItemID] {
			out = append(out, item)
		}
	}
	return out, nil
}

// Page is a list of reviews with their images and shop replies.
type Page struct {
	Reviews []*domain.Review
	Images  map[string][]*domain.Image
	Replies map[string]*domain.Reply
}

func (u *ReviewUseCase) page(ctx context.Context, items []*domain.Review, err error) (*Page, error) {
	if err != nil {
		return nil, failure(err)
	}
	ids := make([]string, 0, len(items))
	for _, v := range items {
		ids = append(ids, v.ID)
	}
	images, err := u.Repo.ImagesFor(ctx, ids)
	if err != nil {
		return nil, failure(err)
	}
	replies, err := u.Repo.RepliesFor(ctx, ids)
	if err != nil {
		return nil, failure(err)
	}
	return &Page{Reviews: items, Images: images, Replies: replies}, nil
}

// ListPublic is the storefront list and its summary; both count exactly
// the reviews the storefront may show.
func (u *ReviewUseCase) ListPublic(ctx context.Context, productID string, rating, limit, offset int) (*Page, domain.Summary, error) {
	summary, err := u.Summary(ctx, productID)
	if err != nil {
		return nil, domain.Summary{}, err
	}
	items, err := u.Repo.ListPublic(ctx, productID, rating, u.ShowUnverified, limit, offset)
	p, err := u.page(ctx, items, err)
	if err != nil {
		return nil, domain.Summary{}, err
	}
	return p, summary, nil
}

func (u *ReviewUseCase) Summary(ctx context.Context, productID string) (domain.Summary, error) {
	if err := validID(productID, "product"); err != nil {
		return domain.Summary{}, err
	}
	s, err := u.Repo.Summary(ctx, productID, u.ShowUnverified)
	return s, failure(err)
}

func (u *ReviewUseCase) ListMine(ctx context.Context, buyerID string, limit, offset int) (*Page, error) {
	items, err := u.Repo.ListBuyer(ctx, buyerID, limit, offset)
	return u.page(ctx, items, err)
}

func (u *ReviewUseCase) ownedShop(ctx context.Context, userID, vendorID string) error {
	if err := validID(vendorID, "shop"); err != nil {
		return err
	}
	return failure(u.Vendors.EnsureOwnedApproved(ctx, userID, vendorID))
}

func (u *ReviewUseCase) ListVendor(ctx context.Context, userID, vendorID, productID string, rating int, replied *bool, limit, offset int) (*Page, error) {
	if err := u.ownedShop(ctx, userID, vendorID); err != nil {
		return nil, err
	}
	if productID != "" {
		if err := validID(productID, "product"); err != nil {
			return nil, err
		}
	}
	items, err := u.Repo.ListVendor(ctx, vendorID, productID, rating, replied, u.ShowUnverified, limit, offset)
	return u.page(ctx, items, err)
}

func (u *ReviewUseCase) VendorSummary(ctx context.Context, userID, vendorID string) (domain.Summary, error) {
	if err := u.ownedShop(ctx, userID, vendorID); err != nil {
		return domain.Summary{}, err
	}
	s, err := u.Repo.VendorSummary(ctx, vendorID, u.ShowUnverified)
	return s, failure(err)
}

var adminStatuses = map[string]bool{"": true, "published": true, "hidden": true}

func (u *ReviewUseCase) ListAdmin(ctx context.Context, f repository.AdminFilter, limit, offset int) (*Page, error) {
	for what, id := range map[string]string{"buyer": f.BuyerID, "shop": f.VendorID, "product": f.ProductID} {
		if id != "" {
			if err := validID(id, what); err != nil {
				return nil, err
			}
		}
	}
	if !adminStatuses[f.Status] {
		return nil, apperror.Validation("status must be published or hidden")
	}
	items, err := u.Repo.ListAdmin(ctx, f, limit, offset)
	return u.page(ctx, items, err)
}

// reviewOfShop locks a published review of the shop for a shop action.
func (u *ReviewUseCase) reviewOfShop(ctx context.Context, reviewID, vendorID string) (*domain.Review, error) {
	v, err := u.Repo.FindForUpdate(ctx, reviewID)
	if err != nil {
		return nil, err
	}
	if v.VendorID != vendorID {
		return nil, apperror.Forbidden("This review does not belong to this shop")
	}
	if v.Status != domain.ReviewPublished {
		return nil, apperror.Conflict("This review is hidden")
	}
	return v, nil
}

// Reply sets the shop's public reply to one of its reviews (audited with
// the previous text).
func (u *ReviewUseCase) Reply(ctx context.Context, userID, vendorID, reviewID, message string) (*domain.Reply, error) {
	message, err := domain.ValidateReply(message)
	if err != nil {
		return nil, err
	}
	if err := validID(reviewID, "review"); err != nil {
		return nil, err
	}
	if err := u.ownedShop(ctx, userID, vendorID); err != nil {
		return nil, err
	}
	reply := &domain.Reply{ReviewID: reviewID, VendorID: vendorID, Message: message}
	err = u.inTx(ctx, func(ctx context.Context) error {
		if _, err := u.reviewOfShop(ctx, reviewID, vendorID); err != nil {
			return err
		}
		previous, err := u.Repo.SetReply(ctx, reply)
		if err != nil {
			return err
		}
		action := "reply_created"
		if previous != nil {
			action = "reply_updated"
		}
		return u.Repo.Audit(ctx, repository.AuditEntry{ActorID: userID, Action: action, EntityType: "review", EntityID: reviewID, ReviewID: &reviewID,
			Changes: map[string]any{"vendor_id": vendorID, "message": []any{previous, message}}})
	})
	if err != nil {
		return nil, err
	}
	return reply, nil
}

// Report lets the shop flag one of its reviews for an admin (audited).
func (u *ReviewUseCase) Report(ctx context.Context, userID, vendorID, reviewID, reasonID string, note *string) (*domain.Report, error) {
	if err := validID(reviewID, "review"); err != nil {
		return nil, err
	}
	if err := validID(reasonID, "reason"); err != nil {
		return nil, err
	}
	note, err := domain.ValidateNote(note, false)
	if err != nil {
		return nil, err
	}
	if err := u.ownedShop(ctx, userID, vendorID); err != nil {
		return nil, err
	}
	var x *domain.Report
	err = u.inTx(ctx, func(ctx context.Context) error {
		if _, err := u.reviewOfShop(ctx, reviewID, vendorID); err != nil {
			return err
		}
		reason, err := u.Repo.FindReason(ctx, reasonID)
		if errors.Is(err, repository.ErrNotFound) || (err == nil && !reason.IsActive) {
			return apperror.Validation("Choose an active moderation reason")
		}
		if err != nil {
			return err
		}
		x = &domain.Report{ReviewID: reviewID, ReportingVendorID: vendorID, ReasonID: reasonID, ReasonCode: reason.Code, ReasonLabel: reason.Label, Note: note}
		if err := u.Repo.CreateReport(ctx, x); err != nil {
			if errors.Is(err, repository.ErrConflict) {
				return apperror.Conflict("This shop already has an open report for this review")
			}
			return err
		}
		return u.Repo.Audit(ctx, repository.AuditEntry{ActorID: userID, Action: "report_created", EntityType: "review", EntityID: reviewID,
			ReviewID: &reviewID, ReportID: &x.ID, ReasonID: &reasonID, Note: note, Changes: map[string]any{"vendor_id": vendorID}})
	})
	if err != nil {
		return nil, err
	}
	return x, nil
}

func (u *ReviewUseCase) ActiveReasons(ctx context.Context) ([]*domain.Reason, error) {
	x, err := u.Repo.ListReasons(ctx, true)
	return x, failure(err)
}

func (u *ReviewUseCase) ListReasons(ctx context.Context) ([]*domain.Reason, error) {
	x, err := u.Repo.ListReasons(ctx, false)
	return x, failure(err)
}

func (u *ReviewUseCase) CreateReason(ctx context.Context, adminID, code, label string, description *string) (*domain.Reason, error) {
	code, label, description, err := domain.ValidateReason(code, label, description)
	if err != nil {
		return nil, err
	}
	if err := u.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	x := &domain.Reason{Code: code, Label: label, Description: description}
	err = u.inTx(ctx, func(ctx context.Context) error {
		if err := u.Repo.CreateReason(ctx, x); err != nil {
			if errors.Is(err, repository.ErrConflict) {
				return apperror.Conflict("A reason with this code already exists")
			}
			return err
		}
		return u.Repo.Audit(ctx, repository.AuditEntry{ActorID: adminID, Action: "reason_created", EntityType: "moderation_reason", EntityID: x.ID,
			Changes: map[string]any{"code": x.Code, "label": x.Label}})
	})
	if err != nil {
		return nil, err
	}
	return x, nil
}

// UpdateReason changes a reason; active nil keeps its current state.
func (u *ReviewUseCase) UpdateReason(ctx context.Context, adminID, id, code, label string, description *string, active *bool) (*domain.Reason, error) {
	if err := validID(id, "reason"); err != nil {
		return nil, err
	}
	code, label, description, err := domain.ValidateReason(code, label, description)
	if err != nil {
		return nil, err
	}
	if err := u.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	var x *domain.Reason
	err = u.inTx(ctx, func(ctx context.Context) error {
		before, err := u.Repo.FindReason(ctx, id)
		if err != nil {
			return err
		}
		after := *before
		after.Code, after.Label, after.Description = code, label, description
		if active != nil {
			after.IsActive = *active
		}
		if err := u.Repo.UpdateReason(ctx, &after); err != nil {
			if errors.Is(err, repository.ErrConflict) {
				return apperror.Conflict("A reason with this code already exists")
			}
			return err
		}
		x = &after
		return u.Repo.Audit(ctx, repository.AuditEntry{ActorID: adminID, Action: "reason_updated", EntityType: "moderation_reason", EntityID: id,
			Changes: map[string]any{"code": []any{before.Code, after.Code}, "label": []any{before.Label, after.Label},
				"is_active": []any{before.IsActive, after.IsActive}}})
	})
	if err != nil {
		return nil, err
	}
	return x, nil
}

func (u *ReviewUseCase) ListReports(ctx context.Context, status, vendorID, productID string, limit, offset int) ([]*domain.Report, error) {
	if status != "" && status != "open" && status != "resolved" {
		return nil, apperror.Validation("status must be open or resolved")
	}
	for what, id := range map[string]string{"shop": vendorID, "product": productID} {
		if id != "" {
			if err := validID(id, what); err != nil {
				return nil, err
			}
		}
	}
	x, err := u.Repo.ListReports(ctx, status, vendorID, productID, limit, offset)
	return x, failure(err)
}

func (u *ReviewUseCase) activeReason(ctx context.Context, reasonID string) error {
	if err := validID(reasonID, "reason"); err != nil {
		return apperror.Validation("Choose an active moderation reason")
	}
	reason, err := u.Repo.FindReason(ctx, reasonID)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && !reason.IsActive) {
		return apperror.Validation("Choose an active moderation reason")
	}
	return err
}

// ResolveReport closes an open report: keep leaves the review as it is,
// hide takes it off the storefront with a reason. Audited with the report.
func (u *ReviewUseCase) ResolveReport(ctx context.Context, adminID, reportID string, decision domain.ReportDecision, reasonID string, note *string) error {
	if err := validID(reportID, "report"); err != nil {
		return err
	}
	if decision != domain.DecisionKeep && decision != domain.DecisionHide {
		return apperror.Validation("Decision must be keep or hide")
	}
	note, err := domain.ValidateNote(note, false)
	if err != nil {
		return err
	}
	if err := u.requireAdmin(ctx, adminID); err != nil {
		return err
	}
	return u.inTx(ctx, func(ctx context.Context) error {
		report, err := u.Repo.FindReportForUpdate(ctx, reportID)
		if err != nil {
			return err
		}
		if report.Status != domain.ReportOpen {
			return apperror.Conflict("This report has already been resolved")
		}
		review, err := u.Repo.FindForUpdate(ctx, report.ReviewID)
		if err != nil {
			return err
		}
		var reason *string
		changes := map[string]any{"decision": decision}
		if decision == domain.DecisionHide {
			if err := u.activeReason(ctx, reasonID); err != nil {
				return err
			}
			reason = &reasonID
			if review.Status == domain.ReviewPublished {
				if err := u.Repo.Hide(ctx, review.ID, reasonID, adminID, note); err != nil {
					return err
				}
				changes["status"] = []any{review.Status, domain.ReviewHidden}
			}
		}
		if err := u.Repo.ResolveReport(ctx, reportID, decision, reason, adminID, note); err != nil {
			return err
		}
		return u.Repo.Audit(ctx, repository.AuditEntry{ActorID: adminID, Action: "report_" + string(decision), EntityType: "review",
			EntityID: review.ID, ReviewID: &review.ID, ReportID: &reportID, ReasonID: reason, Note: note, Changes: changes})
	})
}

// Hide takes a published review off the storefront (kept, audited).
func (u *ReviewUseCase) Hide(ctx context.Context, adminID, reviewID, reasonID string, note *string) error {
	if err := validID(reviewID, "review"); err != nil {
		return err
	}
	note, err := domain.ValidateNote(note, true)
	if err != nil {
		return err
	}
	if err := u.requireAdmin(ctx, adminID); err != nil {
		return err
	}
	return u.inTx(ctx, func(ctx context.Context) error {
		review, err := u.Repo.FindForUpdate(ctx, reviewID)
		if err != nil {
			return err
		}
		if review.Status != domain.ReviewPublished {
			return apperror.Conflict("This review is already hidden")
		}
		if err := u.activeReason(ctx, reasonID); err != nil {
			return err
		}
		if err := u.Repo.Hide(ctx, reviewID, reasonID, adminID, note); err != nil {
			return err
		}
		return u.Repo.Audit(ctx, repository.AuditEntry{ActorID: adminID, Action: "review_hidden", EntityType: "review", EntityID: reviewID,
			ReviewID: &reviewID, ReasonID: &reasonID, Note: note, Changes: map[string]any{"status": []any{domain.ReviewPublished, domain.ReviewHidden}}})
	})
}

// Restore publishes a hidden review again (note required, audited).
func (u *ReviewUseCase) Restore(ctx context.Context, adminID, reviewID string, note *string) error {
	if err := validID(reviewID, "review"); err != nil {
		return err
	}
	note, err := domain.ValidateNote(note, true)
	if err != nil {
		return err
	}
	if err := u.requireAdmin(ctx, adminID); err != nil {
		return err
	}
	return u.inTx(ctx, func(ctx context.Context) error {
		review, err := u.Repo.FindForUpdate(ctx, reviewID)
		if err != nil {
			return err
		}
		if review.Status != domain.ReviewHidden {
			return apperror.Conflict("This review is not hidden")
		}
		if err := u.Repo.Restore(ctx, reviewID); err != nil {
			return err
		}
		return u.Repo.Audit(ctx, repository.AuditEntry{ActorID: adminID, Action: "review_restored", EntityType: "review", EntityID: reviewID,
			ReviewID: &reviewID, Note: note, Changes: map[string]any{"status": []any{domain.ReviewHidden, domain.ReviewPublished},
				"hidden_reason_id": review.HiddenReasonID}})
	})
}

// Operations is the moderation backlog and upload health (re-verified).
func (u *ReviewUseCase) Operations(ctx context.Context, adminID string) (map[string]int64, error) {
	if err := u.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	counts, err := u.Repo.Operations(ctx)
	return counts, failure(err)
}

// UploadImage stores a photo for the buyer's own review: checked,
// re-encoded without camera metadata, at most five per review. The upload
// is recorded before the object is stored, so an object whose metadata
// write fails is removed by CleanUploads instead of being left behind.
func (u *ReviewUseCase) UploadImage(ctx context.Context, buyerID, reviewID, contentType string, data []byte) (*domain.Image, error) {
	if err := validID(reviewID, "review"); err != nil {
		return nil, err
	}
	own := func(v *domain.Review) error {
		if v.BuyerID != buyerID {
			return apperror.NotFound("Review resource not found")
		}
		if v.Status != domain.ReviewPublished {
			return apperror.Conflict("This review is hidden")
		}
		return nil
	}
	v, err := u.Repo.Find(ctx, reviewID)
	if err != nil {
		return nil, failure(err)
	}
	if err := own(v); err != nil {
		return nil, err
	}
	prepared, err := u.prepare(ctx, contentType, data)
	if err != nil {
		return nil, err
	}
	key := path.Join("reviews", reviewID, uuid.NewString()+prepared.Ext)
	err = u.inTx(ctx, func(ctx context.Context) error {
		v, err := u.Repo.FindForUpdate(ctx, reviewID)
		if err != nil {
			return err
		}
		if err := own(v); err != nil {
			return err
		}
		n, err := u.Repo.CountImageSlots(ctx, reviewID)
		if err != nil {
			return err
		}
		if n >= domain.MaxImages() {
			return apperror.Validation("A review can have at most 5 images")
		}
		return u.Repo.AddUpload(ctx, reviewID, key)
	})
	if err != nil {
		return nil, err
	}
	url, err := u.Store.Upload(ctx, key, prepared.Data, prepared.ContentType)
	if err != nil {
		u.Log.Error().Err(err).Str("review_id", reviewID).Msg("review_image_upload_failed")
		u.discard(ctx, key)
		return nil, apperror.Internal(err)
	}
	image := &domain.Image{ReviewID: reviewID, ObjectKey: key, URL: url, ContentType: prepared.ContentType, SizeBytes: int64(len(prepared.Data))}
	err = u.inTx(ctx, func(ctx context.Context) error {
		if _, err := u.Repo.FindForUpdate(ctx, reviewID); err != nil {
			return err
		}
		return u.Repo.RecordImage(ctx, image)
	})
	if err != nil {
		u.Log.Error().Err(err).Str("review_id", reviewID).Msg("review_image_record_failed")
		u.discard(ctx, key)
		return nil, err
	}
	return image, nil
}

func (u *ReviewUseCase) prepare(ctx context.Context, contentType string, data []byte) (*domain.PreparedImage, error) {
	select {
	case u.imageSlots <- struct{}{}:
		defer func() { <-u.imageSlots }()
	case <-ctx.Done():
		return nil, apperror.Internal(ctx.Err())
	}
	return domain.PrepareImage(contentType, data)
}

// discard removes an object whose upload failed; if that fails too, the
// upload stays recorded and CleanUploads retries.
func (u *ReviewUseCase) discard(ctx context.Context, key string) {
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := u.Store.Delete(dctx, key); err != nil {
		return
	}
	if err := u.Repo.DeleteUpload(dctx, key); err != nil {
		u.Log.Warn().Err(err).Msg("review_upload_close_failed")
	}
}

// maxCleanupAttempts parks an upload whose object cannot be removed after
// about a day of retries.
const maxCleanupAttempts = 30

// CleanUploads removes the objects of uploads left behind (metadata write
// failed, process stopped), retrying with backoff and parking after
// maxCleanupAttempts. It returns how many it closed.
func (u *ReviewUseCase) CleanUploads(ctx context.Context) (int, error) {
	uploads, err := u.Repo.ClaimStaleUploads(ctx, 50)
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, up := range uploads {
		if !up.Recorded {
			if err := u.Store.Delete(ctx, up.ObjectKey); err != nil {
				attempt := up.Attempts + 1
				backoff := time.Duration(1<<min(attempt, 6)) * time.Minute
				park := attempt >= maxCleanupAttempts
				if ferr := u.Repo.UploadCleanupFailed(ctx, up.ObjectKey, "object storage: delete failed", u.Now().Add(backoff), park); ferr != nil {
					return closed, ferr
				}
				event := u.Log.Warn()
				if park {
					event = u.Log.Error()
				}
				event.Err(err).Int("attempt", attempt).Bool("parked", park).Msg("review_image_cleanup_failed")
				continue
			}
		}
		if err := u.Repo.DeleteUpload(ctx, up.ObjectKey); err != nil {
			return closed, err
		}
		closed++
	}
	if closed > 0 {
		u.Log.Info().Int("uploads", closed).Msg("review_image_uploads_cleaned")
	}
	return closed, nil
}

// BackfillLabels resolves the author label of reviews written before
// labels were stored, a batch at a time; Identity being down leaves the
// rest for the next round.
func (u *ReviewUseCase) BackfillLabels(ctx context.Context, batch int) (int, error) {
	tasks, err := u.Repo.PendingLabels(ctx, batch)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, t := range tasks {
		label, checked := u.authorLabel(ctx, t.BuyerID)
		if !checked {
			break
		}
		if err := u.Repo.SetLabel(ctx, t.ReviewID, label); err != nil {
			return done, err
		}
		done++
	}
	return done, nil
}
