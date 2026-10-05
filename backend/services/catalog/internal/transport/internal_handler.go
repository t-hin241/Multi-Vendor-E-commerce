package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/usecase"
)

// InternalHandler serves authenticated service-to-service product lookups.
type InternalHandler struct {
	products *usecase.ProductUseCase
	log      zerolog.Logger
}

func NewInternalHandler(products *usecase.ProductUseCase, log zerolog.Logger) *InternalHandler {
	return &InternalHandler{products: products, log: log}
}

// CheckoutSnapshot is read-only despite POST: the bounded request carries
// two ID sets without URL-length limits. Only Order may call this endpoint.
func (h *InternalHandler) CheckoutSnapshot(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var in struct {
		ProductIDs []string `json:"product_ids"`
		VariantIDs []string `json:"variant_ids"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpresponse.HandleError(c, h.log, apperror.Validation("Invalid checkout snapshot request"))
		return
	}
	snapshot, err := h.products.ReadCheckout(c.Request.Context(), in.ProductIDs, in.VariantIDs)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	products := make([]internalProductResponse, 0, len(snapshot.Products))
	for _, p := range snapshot.Products {
		products = append(products, internalProductResponse{
			ID: p.ID, VendorID: p.VendorID, Name: p.Name, Version: p.Version,
			PriceAmount: p.PriceAmount, Currency: p.Currency, Status: string(p.Status),
			IsVisible:   p.Status == domain.StatusApproved && p.IsActive && p.EnforcedVersion == p.Version,
			HasVariants: p.HasVariants, PackageWeightGrams: p.PackageWeightGrams,
		})
	}
	httpresponse.OK(c, http.StatusOK, struct {
		Products []internalProductResponse `json:"products"`
		Variants []domain.CheckoutVariant  `json:"variants"`
	}{products, snapshot.Variants})
}

type internalProductResponse struct {
	Version            int64  `json:"version"`
	ID                 string `json:"id"`
	VendorID           string `json:"vendor_id"`
	Name               string `json:"name"`
	PriceAmount        int64  `json:"price_amount"`
	Currency           string `json:"currency"`
	Status             string `json:"status"`
	IsVisible          bool   `json:"is_visible"`
	HasVariants        bool   `json:"has_variants"`
	PackageWeightGrams *int64 `json:"package_weight_grams,omitempty"`
}

// GetByID serves Inventory (verifying a vendor owns a product before it
// touches stock, and reading Status to gate a restock request on the
// product being approved) and Cart/Order (validating a product is sellable,
// reading its current price for a checkout snapshot, checking has_variants
// to require a variant selection, and reading package_weight_grams to quote
// a shipment's fee), regardless of the product's moderation status —
// callers decide what to do with is_visible/status. PackageWeightGrams is a
// pointer: a category that doesn't require packaging info must stay
// distinguishable from a genuine 0.
func (h *InternalHandler) GetByID(c *gin.Context) {
	p, hasVariants, packageWeightGrams, err := h.products.GetByIDForOwnerLookup(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, internalProductResponse{
		ID:                 p.ID,
		Version:            p.Version,
		VendorID:           p.VendorID,
		Name:               p.Name,
		PriceAmount:        p.PriceAmount,
		Currency:           p.Currency,
		Status:             string(p.Status),
		IsVisible:          p.IsPubliclyVisible() && p.EnforcedVersion == p.Version,
		HasVariants:        hasVariants,
		PackageWeightGrams: packageWeightGrams,
	})
}

type internalVariantOwnerResponse struct {
	ID        string                  `json:"id"`
	ProductID string                  `json:"product_id"`
	VendorID  string                  `json:"vendor_id"`
	SKU       string                  `json:"sku"`
	Options   []variantOptionResponse `json:"options"`
}

// GetVariantOwner serves two callers against the same lookup: Inventory's
// ownership check for variant-scoped stock operations (which only needs
// product_id/vendor_id — a client-supplied product_id is never trusted for
// a variant-scoped request, only what this endpoint resolves), and
// Cart/Order's need for the variant's SKU/option labels to validate and
// display a cart or order line.
func (h *InternalHandler) GetVariantOwner(c *gin.Context) {
	vendorID, productID, sku, options, err := h.products.GetVariantOwner(c.Request.Context(), c.Param("variantId"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, internalVariantOwnerResponse{
		ID:        c.Param("variantId"),
		ProductID: productID,
		VendorID:  vendorID,
		SKU:       sku,
		Options:   toVariantOptionResponseListFromDetails(options),
	})
}
