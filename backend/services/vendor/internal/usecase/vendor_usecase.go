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
	vendors   VendorRepositoryPort
	auditLogs AuditLogRepositoryPort
	store     ObjectStore
	log       zerolog.Logger
	ops       Operations
}

func NewVendorUseCase(vendors VendorRepositoryPort, auditLogs AuditLogRepositoryPort, store ObjectStore, log zerolog.Logger, ops Operations) *VendorUseCase {
	return &VendorUseCase{vendors: vendors, auditLogs: auditLogs, store: store, log: log, ops: ops}
}

// Apply creates a new shop application for userID. A user may own any
// number of shops (1:N) — this is how both the first shop and every
// additional one get created, each going through its own pending →
// approved/rejected review independently of any other shop the same user
// owns.
func (uc *VendorUseCase) apply(ctx context.Context, userID, shopName, description string) (*domain.Vendor, error) {
	if len(description) > 4000 {
		return nil, apperror.Validation("Shop description must not exceed 4000 bytes")
	}
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
func (uc *VendorUseCase) ListByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Vendor, error) {
	if err := uc.ops.Actors.RequireRole(ctx, userID, "vendor"); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, apperror.Validation("Invalid pagination")
	}
	vendors, err := uc.vendors.ListByUserID(ctx, userID, limit, offset)
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
	if err := uc.ops.Actors.RequireRole(ctx, userID, "vendor"); err != nil {
		return nil, err
	}
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

func (uc *VendorUseCase) updateProfile(ctx context.Context, userID, vendorID, shopName, description, policyText string) (*domain.Vendor, error) {
	if err := domain.ValidateApplication(shopName); err != nil {
		return nil, err
	}

	v, err := uc.GetOwned(ctx, userID, vendorID)
	if err != nil {
		return nil, err
	}
	if len(description) > 4000 || len(policyText) > 10000 || (v.IsApproved() && strings.TrimSpace(description) == "") {
		return nil, apperror.Validation("Invalid shop description or policy length")
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
		if e := uc.store.Delete(ctx, *oldKey); e != nil {
			uc.log.Warn().Str("vendor_id", v.ID).Msg("vendor_media_cleanup_failed")
		}
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
		if e := uc.store.Delete(ctx, *oldKey); e != nil {
			uc.log.Warn().Str("vendor_id", v.ID).Msg("vendor_media_cleanup_failed")
		}
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

func (uc *VendorUseCase) ListApplications(ctx context.Context, actorID, status string, limit, offset int) ([]*domain.Vendor, error) {
	if err := uc.ops.Actors.RequireRole(ctx, actorID, "admin"); err != nil {
		return nil, err
	}
	if !validStatus(status) || limit < 1 || limit > 100 || offset < 0 {
		return nil, apperror.Validation("Invalid list filters")
	}
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

func (uc *VendorUseCase) Approve(ctx context.Context, vendorID, actorID string) (*domain.Vendor, error) {
	return uc.decide(ctx, vendorID, actorID, domain.StatusApproved, "", false)
}
func (uc *VendorUseCase) Reject(ctx context.Context, vendorID, actorID, reason string) (*domain.Vendor, error) {
	return uc.decide(ctx, vendorID, actorID, domain.StatusRejected, reason, false)
}
func (uc *VendorUseCase) Suspend(ctx context.Context, vendorID, actorID, reason string) (*domain.Vendor, error) {
	return uc.decide(ctx, vendorID, actorID, domain.StatusSuspended, reason, false)
}
func (uc *VendorUseCase) Restore(ctx context.Context, vendorID, actorID, reason string) (*domain.Vendor, error) {
	return uc.decide(ctx, vendorID, actorID, domain.StatusApproved, reason, true)
}
func (uc *VendorUseCase) ListAuditLog(ctx context.Context, actorID, vendorID string, limit, offset int) ([]*domain.AuditLog, error) {
	if err := uc.ops.Actors.RequireRole(ctx, actorID, "admin"); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, apperror.Validation("Invalid pagination")
	}
	rows, err := uc.auditLogs.List(ctx, vendorID, limit, offset)
	return rows, wrap(err)
}

func (uc *VendorUseCase) UpdateProfile(ctx context.Context, userID, vendorID, name, description, policy string) (out *domain.Vendor, err error) {
	err = uc.ops.Tx.Run(ctx, func(ctx context.Context) error {
		var e error
		out, e = uc.updateProfile(ctx, userID, vendorID, name, description, policy)
		return e
	})
	return out, wrap(err)
}
