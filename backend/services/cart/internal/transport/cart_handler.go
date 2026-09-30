package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/cart/internal/usecase"
)

// CartHandler serves the buyer's own cart. The buyer id always comes from
// the verified access token, never from the request.
type CartHandler struct {
	cart *usecase.CartUseCase
	log  zerolog.Logger
}

func NewCartHandler(cart *usecase.CartUseCase, log zerolog.Logger) *CartHandler {
	return &CartHandler{cart: cart, log: log}
}

func (h *CartHandler) View(c *gin.Context) {
	page, ok := pageParams(c)
	if !ok {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "limit must be between 1 and 50 and offset between 0 and 1000")
		return
	}
	h.respondWithCart(c, http.StatusOK, page)
}

func (h *CartHandler) AddItem(c *gin.Context) {
	var req addItemRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := h.cart.AddItem(c.Request.Context(), middleware.GetUserID(c), req.ProductID, req.VariantID, req.Quantity, req.ExpectedVersion); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	h.respondWithCart(c, http.StatusCreated, defaultPage())
}

func (h *CartHandler) SetItemQuantity(c *gin.Context) {
	productID, variantID, ok := lineTarget(c)
	if !ok {
		return
	}
	var req setQuantityRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := h.cart.SetItemQuantity(c.Request.Context(), middleware.GetUserID(c), productID, variantID, *req.Quantity, req.ExpectedVersion); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	h.respondWithCart(c, http.StatusOK, defaultPage())
}

func (h *CartHandler) RemoveItem(c *gin.Context) {
	productID, variantID, ok := lineTarget(c)
	if !ok {
		return
	}
	expected, ok := expectedVersionParam(c)
	if !ok {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "expected_version must be a positive integer")
		return
	}
	if err := h.cart.RemoveItem(c.Request.Context(), middleware.GetUserID(c), productID, variantID, expected); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"removed": true})
}

func (h *CartHandler) Clear(c *gin.Context) {
	expected, ok := expectedVersionParam(c)
	if !ok {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "expected_version must be a positive integer")
		return
	}
	if err := h.cart.Clear(c.Request.Context(), middleware.GetUserID(c), expected); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"cleared": true})
}

// ConfirmPrices records that the buyer accepted the current price of the
// listed lines (the price they were shown), clearing the price-changed flag.
func (h *CartHandler) ConfirmPrices(c *gin.Context) {
	var req confirmPricesRequest
	if !bindJSON(c, &req) {
		return
	}
	confirmations := make([]usecase.PriceConfirmation, 0, len(req.Lines))
	for _, l := range req.Lines {
		confirmations = append(confirmations, usecase.PriceConfirmation{LineID: l.LineID, PriceAmount: l.PriceAmount, Currency: l.Currency})
	}
	if err := h.cart.ConfirmPrices(c.Request.Context(), middleware.GetUserID(c), req.ExpectedVersion, confirmations); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	h.respondWithCart(c, http.StatusOK, defaultPage())
}

func (h *CartHandler) respondWithCart(c *gin.Context, status int, page usecase.Page) {
	view, err := h.cart.View(c.Request.Context(), middleware.GetUserID(c), page)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, status, toCartResponse(view))
}

func defaultPage() usecase.Page { return usecase.Page{Limit: usecase.DefaultPageLimit} }

// lineTarget reads and validates the product id path param and optional
// variant id query param that name a cart line.
func lineTarget(c *gin.Context) (string, *string, bool) {
	productID := c.Param("productID")
	if !validUUID(productID) {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "product_id must be a valid id")
		return "", nil, false
	}
	variantID, ok := optionalVariantID(c)
	if !ok {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "variant_id must be a valid id")
		return "", nil, false
	}
	return productID, variantID, true
}
