package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/catalog/internal/usecase"
)

type AdminHandler struct {
	products *usecase.ProductUseCase
	log      zerolog.Logger
}

func NewAdminHandler(products *usecase.ProductUseCase, log zerolog.Logger) *AdminHandler {
	return &AdminHandler{products: products, log: log}
}

func (h *AdminHandler) ListForModeration(c *gin.Context) {
	limit, offset := paginationParams(c)

	products, err := h.products.ListForModeration(c.Request.Context(), c.Query("status"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toProductResponseList(products))
}

// GetForModeration returns the full submission behind a product row —
// images, media, variants and stock — so admin's approve/reject decision is
// informed by what the vendor was actually required to supply.
func (h *AdminHandler) GetForModeration(c *gin.Context) {
	p, images, media, attributeValues, variants, plainStockQuantity, err := h.products.GetForModeration(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toProductResponseForModeration(p, images, media, attributeValues, variants, plainStockQuantity))
}

// GetAuditLog returns the full approve/reject decision history for one
// product, newest first.
func (h *AdminHandler) GetAuditLog(c *gin.Context) {
	entries, err := h.products.ListAuditLog(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toAuditLogEntryResponseList(entries))
}

func (h *AdminHandler) Approve(c *gin.Context) {
	p, err := h.products.Approve(c.Request.Context(), c.Param("id"), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toProductResponse(p))
}

func (h *AdminHandler) Reject(c *gin.Context) {
	var req rejectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	p, err := h.products.Reject(c.Request.Context(), c.Param("id"), middleware.GetUserID(c), req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toProductResponse(p))
}
