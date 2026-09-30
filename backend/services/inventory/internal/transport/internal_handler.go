package transport

import (
	"errors"
	"net/http"
	"shopee/backend/pkg/apperror"
	"strings"

	"github.com/google/uuid"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
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

	_, err := h.inventory.Reserve(c.Request.Context(), req.OrderID, lines)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	h.operationResponse(c, req.OrderID, http.StatusCreated)
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

	if len(variantIDs) > 100 {
		httpresponse.HandleError(c, h.log, apperror.Validation("Maximum 100 IDs"))
		return
	}
	for _, id := range variantIDs {
		if _, err := uuid.Parse(id); err != nil {
			httpresponse.HandleError(c, h.log, apperror.Validation("Invalid ID"))
			return
		}
	}

	stock, err := h.inventory.GetStockForVariants(c.Request.Context(), variantIDs)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toVariantStockResponseList(stock))
}

// CheckStockReadiness serves Catalog's SubmitForReview completeness gate: a
// plain product needs its own stocked inventory row, a variant product
// needs every listed variant stocked.
func (h *InternalHandler) CheckStockReadiness(c *gin.Context) {
	raw := strings.Split(c.Query("variant_ids"), ",")
	if len(raw) > 100 {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "At most 100 variants are allowed")
		return
	}
	variantIDs := make([]string, 0, len(raw))
	for _, id := range raw {
		id = strings.TrimSpace(id)
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid variant ID")
				return
			}
			variantIDs = append(variantIDs, id)
		}
	}

	ready, err := h.inventory.CheckStockReadiness(c.Request.Context(), c.Param("productID"), variantIDs)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, stockReadinessResponse{Ready: ready})
}

// GetProductStock serves Catalog's admin moderation detail view for a
// non-variant product's current stock.
func (h *InternalHandler) GetProductStock(c *gin.Context) {
	qty, exists, err := h.inventory.GetProductStock(c.Request.Context(), c.Param("productID"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, productStockResponse{AvailableQuantity: qty, Exists: exists})
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

	h.operationResponse(c, req.OrderID, http.StatusOK)
}

// RestockReturn puts a received return's units back into stock: 201 the
// first time, 200 for a replay of the same return.
func (h *InternalHandler) RestockReturn(c *gin.Context) {
	var req returnRestockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "return_id, product_id and a positive quantity are required")
		return
	}
	replayed, err := h.inventory.RestockReturn(c.Request.Context(), req.ReturnID, req.ProductID, req.VariantID, req.Quantity)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	} else {
		h.log.Info().Str("return_id", req.ReturnID).Str("product_id", req.ProductID).Int64("quantity", req.Quantity).Msg("inventory_return_restocked")
	}
	httpresponse.OK(c, status, gin.H{"return_id": req.ReturnID, "replayed": replayed})
}

func (h *InternalHandler) Commit(c *gin.Context) {
	var req releaseRequest // same {order_id} shape
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	if err := h.inventory.Commit(c.Request.Context(), req.OrderID); err != nil {
		// A refused commit means money may have been captured for stock
		// that is not held any more (expired, released or never reserved):
		// it must reach reconciliation, so it is always logged.
		var appErr *apperror.Error
		if errors.As(err, &appErr) && appErr.Code == apperror.CodeConflict {
			h.log.Warn().Str("order_id", req.OrderID).Str("request_id", middleware.GetRequestID(c)).
				Str("reason", appErr.Message).Msg("inventory_commit_conflict")
		}
		httpresponse.HandleError(c, h.log, err)
		return
	}

	h.operationResponse(c, req.OrderID, http.StatusOK)
}

func (h *InternalHandler) Operation(c *gin.Context) { h.operationResponse(c, c.Param("orderID"), 200) }
func (h *InternalHandler) operationResponse(c *gin.Context, id string, status int) {
	if _, err := uuid.Parse(id); err != nil {
		httpresponse.Error(c, 400, "validation_error", "Invalid order ID")
		return
	}
	o, err := h.inventory.Operation(c.Request.Context(), id)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, status, o)
}
