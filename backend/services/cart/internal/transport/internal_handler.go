package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/cart/internal/domain"
	"shopee/backend/services/cart/internal/usecase"
)

// InternalHandler serves Order's checkout contract. Routes are behind the
// service key; the buyer is named in the path by Order, which has already
// authenticated them, and every operation is bound to that buyer.
type InternalHandler struct {
	cart *usecase.CartUseCase
	log  zerolog.Logger
}

func NewInternalHandler(cart *usecase.CartUseCase, log zerolog.Logger) *InternalHandler {
	return &InternalHandler{cart: cart, log: log}
}

// CreateSnapshot: POST /internal/carts/:buyerId/checkout-snapshots
func (h *InternalHandler) CreateSnapshot(c *gin.Context) {
	buyerID := c.Param("buyerId")
	if !validUUID(buyerID) {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "buyer_id must be a valid id")
		return
	}
	var req snapshotRequest
	if !bindJSON(c, &req) {
		return
	}
	op, replayed, err := h.cart.CreateCheckoutSnapshot(c.Request.Context(), buyerID, req.OperationID, req.ExpectedVersion)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	httpresponse.OK(c, status, toSnapshotResponse(op, replayed))
}

// Consume: POST /internal/carts/:buyerId/checkout-snapshots/:operationId/consume
func (h *InternalHandler) Consume(c *gin.Context) {
	buyerID, operationID := c.Param("buyerId"), c.Param("operationId")
	if !validUUID(buyerID) || !validUUID(operationID) {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "buyer_id and operation_id must be valid ids")
		return
	}
	var req consumeRequest
	if !bindJSON(c, &req) {
		return
	}
	lines := make([]domain.ConsumeLine, 0, len(req.Lines))
	for _, l := range req.Lines {
		lines = append(lines, domain.ConsumeLine{LineID: l.LineID, Quantity: l.Quantity})
	}
	receipt, replayed, err := h.cart.ConsumeCheckout(c.Request.Context(), buyerID, operationID, lines)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	h.log.Info().Str("operation_id", operationID).Bool("replayed", replayed).Int64("cart_version", receipt.CartVersion).Msg("cart_checkout_consumed")
	httpresponse.OK(c, http.StatusOK, consumeResponse{OperationID: operationID, CartVersion: receipt.CartVersion,
		ConsumedAt: receipt.ConsumedAt, Replayed: replayed, Lines: receipt.Lines})
}
