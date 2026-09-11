package usecase

import (
	"context"

	"shopee/backend/services/vendorsvc/internal/domain"
)

type VendorRepositoryPort interface {
	Create(ctx context.Context, v *domain.Vendor) error
	FindByUserID(ctx context.Context, userID string) (*domain.Vendor, error)
	FindByID(ctx context.Context, id string) (*domain.Vendor, error)
	ListByStatus(ctx context.Context, status string, limit, offset int) ([]*domain.Vendor, error)
	ListByIDs(ctx context.Context, ids []string) ([]*domain.Vendor, error)
	UpdateProfile(ctx context.Context, id, shopName, description string) error
	UpdateStatus(ctx context.Context, id string, status domain.Status, approvedBy string, rejectionReason *string) error
}

type AuditLogRepositoryPort interface {
	Create(ctx context.Context, vendorID, actorUserID, action string, reason *string) error
}

// NotificationGateway lets Vendor tell an applicant about an approval
// decision without owning any notification data itself. Every call is
// best-effort — see VendorUseCase.notify.
type NotificationGateway interface {
	Notify(ctx context.Context, userID, notifType, referenceID string) error
}

type VendorAddressRepositoryPort interface {
	Create(ctx context.Context, a *domain.VendorAddress) error
	FindByID(ctx context.Context, id string) (*domain.VendorAddress, error)
	ListForVendor(ctx context.Context, vendorID string) ([]*domain.VendorAddress, error)
	FindDefaultForVendor(ctx context.Context, vendorID string) (*domain.VendorAddress, error)
	Update(ctx context.Context, id string, a *domain.VendorAddress) error
	Delete(ctx context.Context, id string) error
	SetDefault(ctx context.Context, vendorID, addressID string) error
}
