package transport

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/inventory/internal/domain"
	"shopee/backend/services/inventory/internal/usecase"
)

// InternalHandler serves the reserve/release contract Order uses at
// checkout and on cancellation/payment failure. Like the other services'
// internal handlers, it is not proxied by the gateway's public route table.
type InternalHandler struct {
	inventory *usecase.InventoryUseCase
	log       zerolog.Logger
}

func NewInternalHandler(inventory *usecase.InventoryUseCase, log zerolog.Logger) *InternalHandler {
	return &InternalHandler{inventory: inventory, log: log}
}

func (h *InternalHandler) Reserve(c *gin.Context) {
	var req reserveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	lines := make([]domain.ReservationLine, 0, len(req.Items))
	for _, item := range req.Items {
		lines = append(lines, domain.ReservationLine{ProductID: item.ProductID, VariantID: item.VariantID, Quantity: item.Quantity})
	}

	reservations, err := h.inventory.Reserve(c.Request.Context(), req.OrderID, lines)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toReservationResponseList(reservations))
}

// GetVariantStock serves Catalog's public product-detail page: current
// stock per variant, for whichever variant_ids it asks about. Unauthenticated
// like the rest of this handler — the response only ever reveals a
// quantity, nothing sensitive.
func (h *InternalHandler) GetVariantStock(c *gin.Context) {
	raw := strings.Split(c.Query("variant_ids"), ",")
	variantIDs := make([]string, 0, len(raw))
	for _, id := range raw {
		id = strings.TrimSpace(id)
		if id != "" {
			variantIDs = append(variantIDs, id)
		}
	}

	stock, err := h.inventory.GetStockForVariants(c.Request.Context(), variantIDs)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toVariantStockResponseList(stock))
}

func (h *InternalHandler) Release(c *gin.Context) {
	var req releaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	if err := h.inventory.Release(c.Request.Context(), req.OrderID); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, gin.H{"released": true})
}

func (h *InternalHandler) Commit(c *gin.Context) {
	var req releaseRequest // same {order_id} shape
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	if err := h.inventory.Commit(c.Request.Context(), req.OrderID); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, gin.H{"committed": true})
}
