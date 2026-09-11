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
