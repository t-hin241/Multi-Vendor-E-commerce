package transport

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/inventory/internal/usecase"
)

type ItemHandler struct {
	inventory *usecase.InventoryUseCase
	log       zerolog.Logger
}

func NewItemHandler(inventory *usecase.InventoryUseCase, log zerolog.Logger) *ItemHandler {
	return &ItemHandler{inventory: inventory, log: log}
}

func (h *ItemHandler) Create(c *gin.Context) {
	var req createItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	item, err := h.inventory.CreateItem(c.Request.Context(), middleware.GetUserID(c), req.ProductID, req.VariantID, req.InitialQuantity)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toItemResponse(item))
}

// Restock now only creates a pending request — see
// InventoryUseCase.RequestRestock — rather than immediately increasing
// available_quantity; an admin must approve it first.
func (h *ItemHandler) Restock(c *gin.Context) {
	var req restockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	productID := c.Param("productID")
	request, err := h.inventory.RequestRestock(c.Request.Context(), middleware.GetUserID(c), &productID, nil, req.Quantity)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toRestockRequestResponse(request))
}

// RestockVariant is Restock's sibling for a variant-scoped stock item —
// kept as a separate route/handler rather than overloading the existing
// :productID path, matching this codebase's preference for explicit
// routes over ambiguous polymorphic ones.
func (h *ItemHandler) RestockVariant(c *gin.Context) {
	var req restockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	variantID := c.Param("variantID")
	request, err := h.inventory.RequestRestock(c.Request.Context(), middleware.GetUserID(c), nil, &variantID, req.Quantity)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toRestockRequestResponse(request))
}

// ListMyRestockRequests lets a vendor see the status of their own pending
// (or already-decided) stock-increase requests.
func (h *ItemHandler) ListMyRestockRequests(c *gin.Context) {
	limit := parseIntDefault(c.Query("limit"), 20, 1, 100)
	offset := parseIntDefault(c.Query("offset"), 0, 0, 1_000_000)

	requests, err := h.inventory.ListMyRestockRequests(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toRestockRequestResponseList(requests))
}

func (h *ItemHandler) ListMine(c *gin.Context) {
	limit := parseIntDefault(c.Query("limit"), 20, 1, 100)
	offset := parseIntDefault(c.Query("offset"), 0, 0, 1_000_000)

	items, err := h.inventory.ListMine(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toItemResponseList(items))
}

func parseIntDefault(raw string, fallback, min, max int) int {
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < min || v > max {
		return fallback
	}
	return v
}
