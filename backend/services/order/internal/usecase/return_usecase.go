package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

type ReturnUseCase struct {
	repo    *repository.ReturnRequestRepository
	vendors VendorGateway
}

func NewReturnUseCase(repo *repository.ReturnRequestRepository, vendors VendorGateway) *ReturnUseCase {
	return &ReturnUseCase{repo: repo, vendors: vendors}
}
func (u *ReturnUseCase) Create(ctx context.Context, buyerID, orderID, itemID, reason string) (*domain.ReturnRequest, error) {
	if len(reason) == 0 || len(reason) > 2000 {
		return nil, apperror.Validation("Return reason must be between 1 and 2000 characters")
	}
	r, err := u.repo.Create(ctx, buyerID, orderID, itemID, reason)
	if errors.Is(err, repository.ErrReturnNotEligible) {
		return nil, apperror.Conflict("Only completed items from your order can be returned")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return r, nil
}
func (u *ReturnUseCase) ListMine(ctx context.Context, buyerID string, limit, offset int) ([]*domain.ReturnRequest, error) {
	return u.repo.ListByBuyer(ctx, buyerID, limit, offset)
}
func (u *ReturnUseCase) AdminList(ctx context.Context, status string, limit, offset int) ([]*domain.ReturnRequest, error) {
	return u.repo.ListByStatus(ctx, status, limit, offset)
}
func (u *ReturnUseCase) ConfirmByVendor(ctx context.Context, id, vendorID, userID string) (*domain.ReturnRequest, error) {
	if _, err := u.vendors.GetApprovedVendorID(ctx, userID, vendorID); err != nil {
		return nil, err
	}
	r, err := u.repo.ConfirmByVendor(ctx, id, vendorID, userID)
	if errors.Is(err, repository.ErrReturnRequestNotFound) {
		return nil, apperror.NotFound("Return request not found or cannot be confirmed")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return r, nil
}
func (u *ReturnUseCase) Decide(ctx context.Context, id, adminID string, approve bool, note string) (*domain.ReturnRequest, error) {
	r, err := u.repo.Decide(ctx, id, adminID, approve, note)
	if errors.Is(err, repository.ErrReturnRequestNotFound) {
		return nil, apperror.NotFound("Return request not found or already decided")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return r, nil
}
