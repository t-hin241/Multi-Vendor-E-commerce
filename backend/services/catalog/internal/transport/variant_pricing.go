package transport

import (
	"net/http"
	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/purchasequote"

	"github.com/gin-gonic/gin"
)

func (h *ProductHandler) PricingCapabilities(c *gin.Context) {
	httpresponse.OK(c, 200, gin.H{"variant_pricing_enabled": h.products.VariantPricingEnabled})
}
func (h *ProductHandler) UpdateVariantPrice(c *gin.Context) {
	var in struct {
		PriceAmount     int64 `json:"price_amount"`
		ExpectedVersion int64 `json:"expected_version"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpresponse.HandleError(c, h.log, apperror.Validation("Invalid SKU price request"))
		return
	}
	v, err := h.products.UpdateVariantPrice(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), c.Param("variantId"), in.PriceAmount, in.ExpectedVersion)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toVariantResponse(v, nil))
}
func (h *InternalHandler) PurchaseQuotes(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var in struct {
		Lines []purchasequote.Input `json:"lines"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpresponse.HandleError(c, h.log, apperror.Validation("Invalid purchase quote request"))
		return
	}
	quote, err := h.products.PurchaseQuotes(c.Request.Context(), in.Lines)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, quote)
}
