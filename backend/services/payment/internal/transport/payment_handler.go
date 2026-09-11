package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/payment/internal/usecase"
)

// PaymentHandler serves the buyer-facing payment flow: starting a payment
// intent for an order, checking its status, and (dev/local only) simulating
// a provider outcome in place of a real hosted checkout page.
type PaymentHandler struct {
	payments *usecase.PaymentUseCase
	log      zerolog.Logger
}

func NewPaymentHandler(payments *usecase.PaymentUseCase, log zerolog.Logger) *PaymentHandler {
	return &PaymentHandler{payments: payments, log: log}
}

func (h *PaymentHandler) CreateIntent(c *gin.Context) {
	var req createIntentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	intent, err := h.payments.CreateIntent(c.Request.Context(), middleware.GetUserID(c), req.OrderID)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toPaymentIntentResponse(intent))
}

func (h *PaymentHandler) Get(c *gin.Context) {
	intent, err := h.payments.GetOwned(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toPaymentIntentResponse(intent))
}

func (h *PaymentHandler) Simulate(c *gin.Context) {
	var req simulateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	intent, err := h.payments.Simulate(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.Outcome == "succeeded", req.FailureReason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toPaymentIntentResponse(intent))
}
