package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/catalog/internal/usecase"
)

// BulkTargetsHandler is the internal lookup of a shop's SKUs and products
// Inventory uses for AF-16 stock imports and exports. Read-only despite
// POST: the lists do not fit a URL.
type BulkTargetsHandler struct {
	Products *usecase.ProductUseCase
	Store    usecase.BulkStore
	Log      zerolog.Logger
}

func (h BulkTargetsHandler) Lookup(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512<<10)
	var in struct {
		VendorID   string   `json:"vendor_id"`
		SKUs       []string `json:"skus"`
		VariantIDs []string `json:"variant_ids"`
		ProductIDs []string `json:"product_ids"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpresponse.HandleError(c, h.Log, apperror.Validation("Invalid target lookup"))
		return
	}
	targets, err := h.Products.VendorTargets(c.Request.Context(), h.Store, in.VendorID, in.SKUs, in.VariantIDs, in.ProductIDs)
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"targets": targets})
}
