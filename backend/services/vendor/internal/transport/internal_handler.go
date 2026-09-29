package transport

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

// InternalHandler serves authenticated ownership and selling-status lookups.
type InternalHandler struct {
	vendors *usecase.VendorUseCase
	log     zerolog.Logger
}

func NewInternalHandler(vendors *usecase.VendorUseCase, log zerolog.Logger) *InternalHandler {
	return &InternalHandler{vendors: vendors, log: log}
}

type vendorStatusResponse struct {
	VendorID string `json:"vendor_id"`
	Status   string `json:"status"`
	Version  int64  `json:"version"`
}

// GetOwnedStatus verifies an active owner and returns the named shop status/version.
func (h *InternalHandler) GetOwnedStatus(c *gin.Context) {
	v, err := h.vendors.GetOwned(c.Request.Context(), c.Param("userID"), c.Param("vendorId"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, vendorStatusResponse{VendorID: v.ID, Status: string(v.Status), Version: v.Version})
}

type vendorNameResponse struct {
	VendorID string `json:"vendor_id"`
	ShopName string `json:"shop_name"`
}

// ListByIDs lets Catalog resolve shop names for a batch of vendor ids in
// one call, for the storefront listing's product cards.
func (h *InternalHandler) ListByIDs(c *gin.Context) {
	var ids []string
	for _, id := range strings.Split(c.Query("ids"), ",") {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}

	if len(ids) > 100 {
		httpresponse.HandleError(c, h.log, apperror.Validation("Maximum 100 shop IDs"))
		return
	}
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			httpresponse.HandleError(c, h.log, apperror.Validation("Invalid shop ID"))
			return
		}
	}
	vendors, err := h.vendors.ListByIDs(c.Request.Context(), ids)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	out := make([]vendorNameResponse, 0, len(vendors))
	for _, v := range vendors {
		out = append(out, vendorNameResponse{VendorID: v.ID, ShopName: v.ShopName})
	}
	httpresponse.OK(c, http.StatusOK, out)
}
