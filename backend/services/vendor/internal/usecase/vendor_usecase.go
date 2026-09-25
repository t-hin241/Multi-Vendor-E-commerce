// Package usecase orchestrates vendor onboarding: applying, viewing/editing
// a shop profile, and the admin approve/reject decision with its audit
// trail.
package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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
	store         ObjectStore
	log           zerolog.Logger
}

func NewVendorUseCase(vendors VendorRepositoryPort, auditLogs AuditLogRepositoryPort, notifications NotificationGateway, store ObjectStore, log zerolog.Logger) *VendorUseCase {
	return &VendorUseCase{vendors: vendors, auditLogs: auditLogs, notifications: notifications, store: store, log: log}
}

// notify is fire-and-forget from every caller's point of view: a
// notification failure is logged and never propagated, since it must never
// roll back the moderation decision that triggered it.
func (uc *VendorUseCase) notify(ctx context.Context, userID, notifType, referenceID string) {
	if err := uc.notifications.Notify(ctx, userID, notifType, referenceID); err != nil {
		uc.log.Error().Err(err).Str("user_id", userID).Str("type", notifType).Msg("failed to send notification")
	}
}

// Apply creates a new shop application for userID. A user may own any
// number of shops (1:N) — this is how both the first shop and every
// additional one get created, each going through its own pending →
// approved/rejected review independently of any other shop the same user
// owns.
func (uc *VendorUseCase) Apply(ctx context.Context, userID, shopName, description string) (*domain.Vendor, error) {
	if err := domain.ValidateApplication(shopName); err != nil {
		return nil, err
	}

	v := &domain.Vendor{UserID: userID, ShopName: strings.TrimSpace(shopName), Description: strings.TrimSpace(description)}
	if err := uc.vendors.Create(ctx, v); err != nil {
		return nil, apperror.Internal(err)
	}
	return v, nil
}

// ListByUserID returns every shop (any status) userID owns — backs the
// "my shops" list the frontend's shop switcher and management page read
// from.
func (uc *VendorUseCase) ListByUserID(ctx context.Context, userID string) ([]*domain.Vendor, error) {
	vendors, err := uc.vendors.ListByUserID(ctx, userID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return vendors, nil
}

// GetOwned resolves vendorID and checks that userID actually owns it — the
// one ownership-check every other vendor-scoped operation (profile update,
// addresses, the internal cross-service lookup) is built on, now that a
// user can own several shops and "the vendor for this user" is no longer
// well-defined on its own. See getOwnedVendor, shared with
// VendorAddressUseCase so the two usecases stay peers rather than one
// depending on the other.
func (uc *VendorUseCase) GetOwned(ctx context.Context, userID, vendorID string) (*domain.Vendor, error) {
	return getOwnedVendor(ctx, uc.vendors, userID, vendorID)
}

func getOwnedVendor(ctx context.Context, vendors VendorRepositoryPort, userID, vendorID string) (*domain.Vendor, error) {
	v, err := vendors.FindByID(ctx, vendorID)
	if err != nil {
		if errors.Is(err, repository.ErrVendorNotFound) {
			return nil, apperror.NotFound("Shop not found")
		}
		return nil, apperror.Internal(err)
	}
	if v.UserID != userID {
		return nil, apperror.Forbidden("You do not have access to this shop")
	}
	return v, nil
}

func (uc *VendorUseCase) UpdateProfile(ctx context.Context, userID, vendorID, shopName, description, policyText string) (*domain.Vendor, error) {
	if err := domain.ValidateApplication(shopName); err != nil {
		return nil, err
	}

	v, err := uc.GetOwned(ctx, userID, vendorID)
	if err != nil {
		return nil, err
	}

	if err := uc.vendors.UpdateProfile(ctx, v.ID, strings.TrimSpace(shopName), strings.TrimSpace(description), strings.TrimSpace(policyText)); err != nil {
		return nil, apperror.Internal(err)
	}

	v.ShopName = strings.TrimSpace(shopName)
	v.Description = strings.TrimSpace(description)
	v.PolicyText = strings.TrimSpace(policyText)
	return v, nil
}

// UploadLogo replaces a shop's logo image, deleting the superseded object
// from storage (best-effort — the DB row, just swapped atomically above, is
// the source of truth, so a storage-delete failure only leaves an orphaned
// blob, same tolerance Catalog's product-image upload already applies).
func (uc *VendorUseCase) UploadLogo(ctx context.Context, userID, vendorID, contentType string, data []byte) (*domain.Vendor, error) {
	v, err := uc.GetOwned(ctx, userID, vendorID)
	if err != nil {
		return nil, err
	}

	ext, err := domain.ValidateImageUpload(contentType, int64(len(data)))
	if err != nil {
		return nil, err
	}

	suffix, err := randomHex(16)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	objectKey := fmt.Sprintf("vendors/%s/logo/%s%s", v.ID, suffix, ext)

	url, err := uc.store.Upload(ctx, objectKey, data, contentType)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	oldKey := v.LogoObjectKey
	if err := uc.vendors.SetLogo(ctx, v.ID, &url, &objectKey); err != nil {
		return nil, apperror.Internal(err)
	}
	if oldKey != nil {
		_ = uc.store.Delete(ctx, *oldKey)
	}

	v.LogoURL, v.LogoObjectKey = &url, &objectKey
	return v, nil
}

// UploadBanner mirrors UploadLogo exactly, for the shop's banner image.
func (uc *VendorUseCase) UploadBanner(ctx context.Context, userID, vendorID, contentType string, data []byte) (*domain.Vendor, error) {
	v, err := uc.GetOwned(ctx, userID, vendorID)
	if err != nil {
		return nil, err
	}

	ext, err := domain.ValidateImageUpload(contentType, int64(len(data)))
	if err != nil {
		return nil, err
	}

	suffix, err := randomHex(16)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	objectKey := fmt.Sprintf("vendors/%s/banner/%s%s", v.ID, suffix, ext)

	url, err := uc.store.Upload(ctx, objectKey, data, contentType)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	oldKey := v.BannerObjectKey
	if err := uc.vendors.SetBanner(ctx, v.ID, &url, &objectKey); err != nil {
		return nil, apperror.Internal(err)
	}
	if oldKey != nil {
		_ = uc.store.Delete(ctx, *oldKey)
	}

	v.BannerURL, v.BannerObjectKey = &url, &objectKey
	return v, nil
}

// GetPublicProfile is the public shop page's read model — only an approved
// shop is visible, same gate Catalog's GetPublicBySlug applies via
// IsPubliclyVisible().
func (uc *VendorUseCase) GetPublicProfile(ctx context.Context, vendorID string) (*domain.Vendor, error) {
	v, err := uc.vendors.FindByID(ctx, vendorID)
	if err != nil {
		if errors.Is(err, repository.ErrVendorNotFound) {
			return nil, apperror.NotFound("Shop not found")
		}
		return nil, apperror.Internal(err)
	}
	if v.Status != domain.StatusApproved {
		return nil, apperror.NotFound("Shop not found")
	}
	return v, nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
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

// ListAuditLog returns the full moderation decision history for one
// vendor. The route is already admin-gated (same as ListApplications), so
// no extra ownership check is needed here.
func (uc *VendorUseCase) ListAuditLog(ctx context.Context, vendorID string) ([]*domain.AuditLog, error) {
	entries, err := uc.auditLogs.List(ctx, vendorID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return entries, nil
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
