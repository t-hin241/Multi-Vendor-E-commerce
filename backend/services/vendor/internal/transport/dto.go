package transport

import (
	"time"

	"shopee/backend/services/vendorsvc/internal/domain"
)

type applyRequest struct {
	ShopName    string `json:"shop_name" binding:"required"`
	Description string `json:"description"`
}

type updateProfileRequest struct {
	ShopName    string `json:"shop_name" binding:"required"`
	Description string `json:"description"`
	PolicyText  string `json:"policy_text"`
}

type rejectRequest struct {
	Reason string `json:"reason" binding:"required"`
}

type vendorResponse struct {
	Version            int64      `json:"version"`
	EnforcedVersion    int64      `json:"enforced_version"`
	EnforcementPending bool       `json:"enforcement_pending"`
	SuspensionReason   *string    `json:"suspension_reason,omitempty"`
	ID                 string     `json:"id"`
	UserID             string     `json:"user_id"`
	ShopName           string     `json:"shop_name"`
	Description        string     `json:"description"`
	Status             string     `json:"status"`
	RejectionReason    *string    `json:"rejection_reason,omitempty"`
	ApprovedAt         *time.Time `json:"approved_at,omitempty"`
	LogoURL            *string    `json:"logo_url,omitempty"`
	BannerURL          *string    `json:"banner_url,omitempty"`
	PolicyText         string     `json:"policy_text"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

func toVendorResponse(v *domain.Vendor) vendorResponse {
	return vendorResponse{
		Version: v.Version, EnforcedVersion: v.EnforcedVersion, EnforcementPending: v.Version > v.EnforcedVersion, SuspensionReason: v.SuspensionReason,
		ID:              v.ID,
		UserID:          v.UserID,
		ShopName:        v.ShopName,
		Description:     v.Description,
		Status:          string(v.Status),
		RejectionReason: v.RejectionReason,
		ApprovedAt:      v.ApprovedAt,
		LogoURL:         v.LogoURL,
		BannerURL:       v.BannerURL,
		PolicyText:      v.PolicyText,
		CreatedAt:       v.CreatedAt,
		UpdatedAt:       v.UpdatedAt,
	}
}

// publicVendorResponse is the public shop page's response shape — no
// user_id/rejection_reason/approved_by, which are internal moderation
// details a public page has no business exposing.
type publicVendorResponse struct {
	ID          string  `json:"id"`
	ShopName    string  `json:"shop_name"`
	Description string  `json:"description"`
	LogoURL     *string `json:"logo_url,omitempty"`
	BannerURL   *string `json:"banner_url,omitempty"`
	PolicyText  string  `json:"policy_text,omitempty"`
	// ShopPolicyVersion is the approved shop policy version shown, when
	// versioned policies are on.
	ShopPolicyVersion *int `json:"shop_policy_version,omitempty"`
}

func toPublicVendorResponse(v *domain.Vendor) publicVendorResponse {
	return publicVendorResponse{
		ID: v.ID, ShopName: v.ShopName, Description: v.Description,
		LogoURL: v.LogoURL, BannerURL: v.BannerURL, PolicyText: v.PolicyText,
	}
}

func toVendorResponseList(vendors []*domain.Vendor) []vendorResponse {
	out := make([]vendorResponse, 0, len(vendors))
	for _, v := range vendors {
		out = append(out, toVendorResponse(v))
	}
	return out
}

type auditLogEntryResponse struct {
	Version     int64     `json:"version"`
	ActorUserID string    `json:"actor_user_id"`
	Action      string    `json:"action"`
	Reason      *string   `json:"reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func toAuditLogEntryResponseList(entries []*domain.AuditLog) []auditLogEntryResponse {
	out := make([]auditLogEntryResponse, 0, len(entries))
	for _, e := range entries {
		out = append(out, auditLogEntryResponse{
			Version:     e.Version,
			ActorUserID: e.ActorUserID, Action: e.Action, Reason: e.Reason, CreatedAt: e.CreatedAt,
		})
	}
	return out
}

type addressRequest struct {
	RecipientName string `json:"recipient_name" binding:"required"`
	Phone         string `json:"phone" binding:"required"`
	Province      string `json:"province" binding:"required"`
	District      string `json:"district" binding:"required"`
	Ward          string `json:"ward" binding:"required"`
	StreetAddress string `json:"street_address" binding:"required"`
}

type vendorAddressResponse struct {
	ID            string `json:"id"`
	RecipientName string `json:"recipient_name"`
	Phone         string `json:"phone"`
	Province      string `json:"province"`
	District      string `json:"district"`
	Ward          string `json:"ward"`
	StreetAddress string `json:"street_address"`
	IsDefault     bool   `json:"is_default"`
}

func toVendorAddressResponse(a *domain.VendorAddress) vendorAddressResponse {
	return vendorAddressResponse{
		ID: a.ID, RecipientName: a.RecipientName, Phone: a.Phone,
		Province: a.Province, District: a.District, Ward: a.Ward, StreetAddress: a.StreetAddress,
		IsDefault: a.IsDefault,
	}
}

func toVendorAddressResponseList(addresses []*domain.VendorAddress) []vendorAddressResponse {
	out := make([]vendorAddressResponse, 0, len(addresses))
	for _, a := range addresses {
		out = append(out, toVendorAddressResponse(a))
	}
	return out
}
