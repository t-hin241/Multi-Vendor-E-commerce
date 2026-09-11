// Package usecase orchestrates vendor onboarding: applying, viewing/editing
// a shop profile, and the admin approve/reject decision with its audit
// trail.
package usecase

import (
	"context"
	"errors"
	"strings"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/repository"
)

type VendorUseCase struct {
	vendors       VendorRepositoryPort
	auditLogs     AuditLogRepositoryPort
	notifications NotificationGateway
	log           zerolog.Logger
}

func NewVendorUseCase(vendors VendorRepositoryPort, auditLogs AuditLogRepositoryPort, notifications NotificationGateway, log zerolog.Logger) *VendorUseCase {
	return &VendorUseCase{vendors: vendors, auditLogs: auditLogs, notifications: notifications, log: log}
}

// notify is fire-and-forget from every caller's point of view: a
// notification failure is logged and never propagated, since it must never
// roll back the moderation decision that triggered it.
func (uc *VendorUseCase) notify(ctx context.Context, userID, notifType, referenceID string) {
	if err := uc.notifications.Notify(ctx, userID, notifType, referenceID); err != nil {
		uc.log.Error().Err(err).Str("user_id", userID).Str("type", notifType).Msg("failed to send notification")
	}
}

func (uc *VendorUseCase) Apply(ctx context.Context, userID, shopName, description string) (*domain.Vendor, error) {
	if err := domain.ValidateApplication(shopName); err != nil {
		return nil, err
	}

	v := &domain.Vendor{UserID: userID, ShopName: strings.TrimSpace(shopName), Description: strings.TrimSpace(description)}
	if err := uc.vendors.Create(ctx, v); err != nil {
		if errors.Is(err, repository.ErrVendorAlreadyExists) {
			return nil, apperror.Conflict("A vendor application already exists for this account")
		}
		return nil, apperror.Internal(err)
	}
	return v, nil
}

func (uc *VendorUseCase) GetByUserID(ctx context.Context, userID string) (*domain.Vendor, error) {
	v, err := uc.vendors.FindByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrVendorNotFound) {
			return nil, apperror.NotFound("No vendor application found for this account")
		}
		return nil, apperror.Internal(err)
	}
	return v, nil
}

func (uc *VendorUseCase) UpdateProfile(ctx context.Context, userID, shopName, description string) (*domain.Vendor, error) {
	if err := domain.ValidateApplication(shopName); err != nil {
		return nil, err
	}

	v, err := uc.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	if err := uc.vendors.UpdateProfile(ctx, v.ID, strings.TrimSpace(shopName), strings.TrimSpace(description)); err != nil {
		return nil, apperror.Internal(err)
	}

	v.ShopName = strings.TrimSpace(shopName)
	v.Description = strings.TrimSpace(description)
	return v, nil
}

func (uc *VendorUseCase) ListApplications(ctx context.Context, status string, limit, offset int) ([]*domain.Vendor, error) {
	vendors, err := uc.vendors.ListByStatus(ctx, status, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return vendors, nil
}

// ListByIDs serves Catalog's batch shop-name lookup for storefront listing
// cards. It returns whatever subset of ids exist, with no status filter —
// the caller (Catalog) already only lists products from approved vendors.
func (uc *VendorUseCase) ListByIDs(ctx context.Context, ids []string) ([]*domain.Vendor, error) {
	vendors, err := uc.vendors.ListByIDs(ctx, ids)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return vendors, nil
}

func (uc *VendorUseCase) Approve(ctx context.Context, vendorID, adminUserID string) (*domain.Vendor, error) {
	v, err := uc.findForDecision(ctx, vendorID)
	if err != nil {
		return nil, err
	}

	if !domain.CanTransition(v.Status, domain.StatusApproved) {
		return nil, apperror.Conflict("Only a pending application can be approved")
	}

	if err := uc.vendors.UpdateStatus(ctx, v.ID, domain.StatusApproved, adminUserID, nil); err != nil {
		return nil, apperror.Internal(err)
	}
	if err := uc.auditLogs.Create(ctx, v.ID, adminUserID, "approved", nil); err != nil {
		return nil, apperror.Internal(err)
	}

	v.Status = domain.StatusApproved
	uc.notify(ctx, v.UserID, "vendor_approved", v.ID)
	return v, nil
}

func (uc *VendorUseCase) Reject(ctx context.Context, vendorID, adminUserID, reason string) (*domain.Vendor, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, apperror.Validation("A rejection reason is required")
	}

	v, err := uc.findForDecision(ctx, vendorID)
	if err != nil {
		return nil, err
	}

	if !domain.CanTransition(v.Status, domain.StatusRejected) {
		return nil, apperror.Conflict("Only a pending application can be rejected")
	}

	if err := uc.vendors.UpdateStatus(ctx, v.ID, domain.StatusRejected, adminUserID, &reason); err != nil {
		return nil, apperror.Internal(err)
	}
	if err := uc.auditLogs.Create(ctx, v.ID, adminUserID, "rejected", &reason); err != nil {
		return nil, apperror.Internal(err)
	}

	v.Status = domain.StatusRejected
	v.RejectionReason = &reason
	uc.notify(ctx, v.UserID, "vendor_rejected", v.ID)
	return v, nil
}

func (uc *VendorUseCase) findForDecision(ctx context.Context, vendorID string) (*domain.Vendor, error) {
	v, err := uc.vendors.FindByID(ctx, vendorID)
	if err != nil {
		if errors.Is(err, repository.ErrVendorNotFound) {
			return nil, apperror.NotFound("Vendor application not found")
		}
		return nil, apperror.Internal(err)
	}
	return v, nil
}
