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
}

type rejectRequest struct {
	Reason string `json:"reason" binding:"required"`
}

type vendorResponse struct {
	ID              string     `json:"id"`
	UserID          string     `json:"user_id"`
	ShopName        string     `json:"shop_name"`
	Description     string     `json:"description"`
	Status          string     `json:"status"`
	RejectionReason *string    `json:"rejection_reason,omitempty"`
	ApprovedAt      *time.Time `json:"approved_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func toVendorResponse(v *domain.Vendor) vendorResponse {
	return vendorResponse{
		ID:              v.ID,
		UserID:          v.UserID,
		ShopName:        v.ShopName,
		Description:     v.Description,
		Status:          string(v.Status),
		RejectionReason: v.RejectionReason,
		ApprovedAt:      v.ApprovedAt,
		CreatedAt:       v.CreatedAt,
		UpdatedAt:       v.UpdatedAt,
	}
}

func toVendorResponseList(vendors []*domain.Vendor) []vendorResponse {
	out := make([]vendorResponse, 0, len(vendors))
	for _, v := range vendors {
		out = append(out, toVendorResponse(v))
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
