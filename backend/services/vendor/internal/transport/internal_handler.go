package transport

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

// InternalHandler serves service-to-service lookups. It is not proxied by
// the gateway's public route table (only /api/vendor/* is), so it is
// reachable only from inside the compose network in this MVP topology.
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
}

// GetStatusByUserID lets Catalog check whether a user is an approved vendor
// before it allows them to publish a product.
func (h *InternalHandler) GetStatusByUserID(c *gin.Context) {
	v, err := h.vendors.GetByUserID(c.Request.Context(), c.Param("userID"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, vendorStatusResponse{VendorID: v.ID, Status: string(v.Status)})
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
