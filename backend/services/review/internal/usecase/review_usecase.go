package usecase

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/review/internal/adapter"
	"shopee/backend/services/review/internal/domain"
	"shopee/backend/services/review/internal/repository"

	"github.com/google/uuid"
)

type ReviewRepository interface {
	Create(context.Context, *domain.Review) error
	Find(context.Context, string) (*domain.Review, error)
	ListPublic(context.Context, string, int, int, int) ([]*domain.Review, error)
	ListBuyer(context.Context, string, int, int) ([]*domain.Review, error)
	ListVendor(context.Context, string, string, int, *bool, int, int) ([]*domain.Review, error)
	ListAdmin(context.Context, string, string, string, string, int, int, int) ([]*domain.Review, error)
	Summary(context.Context, string) (domain.Summary, error)
	VendorSummary(context.Context, string) (domain.Summary, error)
	CreateImage(context.Context, *domain.Image) error
	CountImages(context.Context, string) (int, error)
	ListImages(context.Context, string) ([]*domain.Image, error)
	GetReply(context.Context, string) (*domain.Reply, error)
	UpsertReply(context.Context, *domain.Reply) error
	ListReasons(context.Context, bool) ([]*domain.Reason, error)
	FindReason(context.Context, string) (*domain.Reason, error)
	CreateReason(context.Context, *domain.Reason) error
	UpdateReason(context.Context, *domain.Reason) error
	CreateReport(context.Context, *domain.Report) error
	ListReports(context.Context, string, string, string, int, int) ([]*domain.Report, error)
	ResolveReport(context.Context, string, domain.ReportDecision, string, string, *string) error
}
type ObjectStore interface {
	Upload(context.Context, string, []byte, string) (string, error)
	Delete(context.Context, string) error
}

type ReviewUseCase struct {
	repo     ReviewRepository
	orders   adapter.OrderGateway
	vendors  adapter.VendorGateway
	identity adapter.IdentityGateway
	store    ObjectStore
}

func NewReviewUseCase(repo ReviewRepository, orders adapter.OrderGateway, vendors adapter.VendorGateway, identity adapter.IdentityGateway, store ObjectStore) *ReviewUseCase {
	return &ReviewUseCase{repo: repo, orders: orders, vendors: vendors, identity: identity, store: store}
}
func internal(err error) error {
	if errors.Is(err, repository.ErrNotFound) {
		return apperror.NotFound("Review resource not found")
	}
	if errors.Is(err, repository.ErrConflict) {
		return apperror.Conflict("This action has already been completed")
	}
	return apperror.Internal(err)
}

func (u *ReviewUseCase) Create(ctx context.Context, buyerID, orderItemID string, rating int, comment string) (*domain.Review, error) {
	comment, err := domain.ValidateReview(rating, comment)
	if err != nil {
		return nil, err
	}
	items, err := u.orders.ListEligible(ctx, buyerID, "")
	if err != nil {
		return nil, err
	}
	var eligible *adapter.EligibleOrderItem
	for i := range items {
		if items[i].OrderItemID == orderItemID {
			eligible = &items[i]
			break
		}
	}
	if eligible == nil {
		return nil, apperror.Forbidden("You can review only products from completed orders")
	}
	v := &domain.Review{BuyerID: buyerID, OrderItemID: eligible.OrderItemID, VendorOrderID: eligible.VendorOrderID, ProductID: eligible.ProductID, VendorID: eligible.VendorID, Rating: rating, Comment: comment}
	if err := u.repo.Create(ctx, v); err != nil {
		return nil, internal(err)
	}
	return v, nil
}
func (u *ReviewUseCase) ListEligibility(ctx context.Context, buyerID, productID string) ([]adapter.EligibleOrderItem, error) {
	items, err := u.orders.ListEligible(ctx, buyerID, productID)
	if err != nil {
		return nil, err
	}
	return items, nil
}
func (u *ReviewUseCase) ListPublic(ctx context.Context, productID string, rating, limit, offset int) ([]*domain.Review, domain.Summary, error) {
	items, err := u.repo.ListPublic(ctx, productID, rating, limit, offset)
	if err != nil {
		return nil, domain.Summary{}, internal(err)
	}
	summary, err := u.repo.Summary(ctx, productID)
	if err != nil {
		return nil, domain.Summary{}, internal(err)
	}
	u.enrichBuyerNames(ctx, items)
	return items, summary, nil
}

// enrichBuyerNames degrades to the generic verified-buyer label when Identity
// is unavailable or an older imported review has no matching user. This
// presentation lookup must not hide legitimate reviews.
func (u *ReviewUseCase) enrichBuyerNames(ctx context.Context, reviews []*domain.Review) {
	resolved := make(map[string]string, len(reviews))
	for _, review := range reviews {
		name, ok := resolved[review.BuyerID]
		if !ok {
			name, _ = u.identity.DisplayName(ctx, review.BuyerID)
			resolved[review.BuyerID] = name
		}
		review.BuyerName = name
	}
}
func (u *ReviewUseCase) ListMine(ctx context.Context, buyerID string, limit, offset int) ([]*domain.Review, error) {
	x, err := u.repo.ListBuyer(ctx, buyerID, limit, offset)
	if err != nil {
		return nil, internal(err)
	}
	return x, nil
}
func (u *ReviewUseCase) ListVendor(ctx context.Context, userID, vendorID, productID string, rating int, replied *bool, limit, offset int) ([]*domain.Review, error) {
	if err := u.vendors.EnsureOwnedApproved(ctx, userID, vendorID); err != nil {
		return nil, err
	}
	x, err := u.repo.ListVendor(ctx, vendorID, productID, rating, replied, limit, offset)
	if err != nil {
		return nil, internal(err)
	}
	return x, nil
}
func (u *ReviewUseCase) VendorSummary(ctx context.Context, userID, vendorID string) (domain.Summary, error) {
	if err := u.vendors.EnsureOwnedApproved(ctx, userID, vendorID); err != nil {
		return domain.Summary{}, err
	}
	s, err := u.repo.VendorSummary(ctx, vendorID)
	if err != nil {
		return domain.Summary{}, internal(err)
	}
	return s, nil
}
func (u *ReviewUseCase) ListAdmin(ctx context.Context, buyerID, vendorID, productID, status string, rating, limit, offset int) ([]*domain.Review, error) {
	x, err := u.repo.ListAdmin(ctx, buyerID, vendorID, productID, status, rating, limit, offset)
	if err != nil {
		return nil, internal(err)
	}
	return x, nil
}
func (u *ReviewUseCase) Images(ctx context.Context, reviewID string) ([]*domain.Image, error) {
	x, err := u.repo.ListImages(ctx, reviewID)
	if err != nil {
		return nil, internal(err)
	}
	return x, nil
}
func (u *ReviewUseCase) GetReply(ctx context.Context, reviewID string) (*domain.Reply, error) {
	x, err := u.repo.GetReply(ctx, reviewID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, internal(err)
	}
	return x, nil
}
func (u *ReviewUseCase) Reply(ctx context.Context, userID, vendorID, reviewID, message string) (*domain.Reply, error) {
	message, err := domain.ValidateReply(message)
	if err != nil {
		return nil, err
	}
	if err := u.vendors.EnsureOwnedApproved(ctx, userID, vendorID); err != nil {
		return nil, err
	}
	v, err := u.repo.Find(ctx, reviewID)
	if err != nil {
		return nil, internal(err)
	}
	if v.VendorID != vendorID {
		return nil, apperror.Forbidden("This review does not belong to this shop")
	}
	reply := &domain.Reply{ReviewID: reviewID, VendorID: vendorID, Message: message}
	if err := u.repo.UpsertReply(ctx, reply); err != nil {
		return nil, internal(err)
	}
	return reply, nil
}
func (u *ReviewUseCase) UploadImage(ctx context.Context, buyerID, reviewID, contentType string, data []byte) (*domain.Image, error) {
	detectedContentType := http.DetectContentType(data)
	if detectedContentType != contentType {
		return nil, apperror.Validation("Image content does not match its declared type")
	}
	ext, err := domain.ValidateImage(contentType, int64(len(data)))
	if err != nil {
		return nil, err
	}
	v, err := u.repo.Find(ctx, reviewID)
	if err != nil {
		return nil, internal(err)
	}
	if v.BuyerID != buyerID {
		return nil, apperror.Forbidden("You do not own this review")
	}
	count, err := u.repo.CountImages(ctx, reviewID)
	if err != nil {
		return nil, internal(err)
	}
	if count >= domain.MaxImages() {
		return nil, apperror.Validation("A review can have at most 5 images")
	}
	key := path.Join("reviews", reviewID, uuid.NewString()+ext)
	url, err := u.store.Upload(ctx, key, data, contentType)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	image := &domain.Image{ReviewID: reviewID, ObjectKey: key, URL: url, ContentType: contentType, SizeBytes: int64(len(data)), Position: count}
	if err := u.repo.CreateImage(ctx, image); err != nil {
		cleanupErr := u.store.Delete(ctx, key)
		if cleanupErr != nil {
			return nil, apperror.Internal(fmt.Errorf("persist review image: %w; cleanup object: %v", err, cleanupErr))
		}
		return nil, internal(err)
	}
	return image, nil
}
func (u *ReviewUseCase) ActiveReasons(ctx context.Context) ([]*domain.Reason, error) {
	x, err := u.repo.ListReasons(ctx, true)
	if err != nil {
		return nil, internal(err)
	}
	return x, nil
}
func (u *ReviewUseCase) ListReasons(ctx context.Context) ([]*domain.Reason, error) {
	x, err := u.repo.ListReasons(ctx, false)
	if err != nil {
		return nil, internal(err)
	}
	return x, nil
}
func (u *ReviewUseCase) CreateReason(ctx context.Context, code, label string, description *string) (*domain.Reason, error) {
	code = strings.TrimSpace(strings.ToLower(code))
	label = strings.TrimSpace(label)
	if code == "" || label == "" {
		return nil, apperror.Validation("Reason code and label are required")
	}
	x := &domain.Reason{Code: code, Label: label, Description: description}
	if err := u.repo.CreateReason(ctx, x); err != nil {
		return nil, internal(err)
	}
	return x, nil
}
func (u *ReviewUseCase) UpdateReason(ctx context.Context, id, code, label string, description *string, active bool) (*domain.Reason, error) {
	x, err := u.repo.FindReason(ctx, id)
	if err != nil {
		return nil, internal(err)
	}
	x.Code = strings.TrimSpace(strings.ToLower(code))
	x.Label = strings.TrimSpace(label)
	x.Description = description
	x.IsActive = active
	if x.Code == "" || x.Label == "" {
		return nil, apperror.Validation("Reason code and label are required")
	}
	if err := u.repo.UpdateReason(ctx, x); err != nil {
		return nil, internal(err)
	}
	return x, nil
}
func (u *ReviewUseCase) Report(ctx context.Context, userID, vendorID, reviewID, reasonID string, note *string) (*domain.Report, error) {
	if err := u.vendors.EnsureOwnedApproved(ctx, userID, vendorID); err != nil {
		return nil, err
	}
	v, err := u.repo.Find(ctx, reviewID)
	if err != nil {
		return nil, internal(err)
	}
	if v.VendorID != vendorID {
		return nil, apperror.Forbidden("This review does not belong to this shop")
	}
	reason, err := u.repo.FindReason(ctx, reasonID)
	if err != nil {
		return nil, internal(err)
	}
	if !reason.IsActive {
		return nil, apperror.Validation("This moderation reason is no longer active")
	}
	x := &domain.Report{ReviewID: reviewID, ReportingVendorID: vendorID, ReasonID: reasonID, ReasonCode: reason.Code, ReasonLabel: reason.Label, Note: note}
	if err := u.repo.CreateReport(ctx, x); err != nil {
		return nil, internal(err)
	}
	return x, nil
}
func (u *ReviewUseCase) ListReports(ctx context.Context, status, vendorID, productID string, limit, offset int) ([]*domain.Report, error) {
	x, err := u.repo.ListReports(ctx, status, vendorID, productID, limit, offset)
	if err != nil {
		return nil, internal(err)
	}
	return x, nil
}
func (u *ReviewUseCase) ResolveReport(ctx context.Context, adminID, reportID string, decision domain.ReportDecision, reasonID string, note *string) error {
	if decision != domain.DecisionKeep && decision != domain.DecisionHide {
		return apperror.Validation("Decision must be keep or hide")
	}
	if decision == domain.DecisionHide {
		reason, err := u.repo.FindReason(ctx, reasonID)
		if err != nil {
			return internal(err)
		}
		if !reason.IsActive {
			return apperror.Validation("This moderation reason is no longer active")
		}
	}
	if err := u.repo.ResolveReport(ctx, reportID, decision, reasonID, adminID, note); err != nil {
		return internal(err)
	}
	return nil
}
