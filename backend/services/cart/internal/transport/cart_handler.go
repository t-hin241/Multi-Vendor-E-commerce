package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/cart/internal/usecase"
)

type CartHandler struct {
	cart *usecase.CartUseCase
	log  zerolog.Logger
}

func NewCartHandler(cart *usecase.CartUseCase, log zerolog.Logger) *CartHandler {
	return &CartHandler{cart: cart, log: log}
}

func (h *CartHandler) View(c *gin.Context) {
	lines, err := h.cart.View(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toCartResponse(lines))
}

func (h *CartHandler) AddItem(c *gin.Context) {
	var req addItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	if err := h.cart.AddItem(c.Request.Context(), middleware.GetUserID(c), req.ProductID, req.VariantID, req.Quantity); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	lines, err := h.cart.View(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toCartResponse(lines))
}

func (h *CartHandler) SetItemQuantity(c *gin.Context) {
	var req setQuantityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	if err := h.cart.SetItemQuantity(c.Request.Context(), middleware.GetUserID(c), c.Param("productID"), optionalVariantID(c), req.Quantity); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	lines, err := h.cart.View(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toCartResponse(lines))
}

func (h *CartHandler) RemoveItem(c *gin.Context) {
	if err := h.cart.RemoveItem(c.Request.Context(), middleware.GetUserID(c), c.Param("productID"), optionalVariantID(c)); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, gin.H{"removed": true})
}

// optionalVariantID reads the ?variant_id= query param used to target a
// variant-scoped line on the shared :productID route — omitted, it targets
// the product-level (no-variant) line, unchanged from before variants
// existed.
func optionalVariantID(c *gin.Context) *string {
	v := c.Query("variant_id")
	if v == "" {
		return nil
	}
	return &v
}

func (h *CartHandler) Clear(c *gin.Context) {
	if err := h.cart.Clear(c.Request.Context(), middleware.GetUserID(c)); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, gin.H{"cleared": true})
}
