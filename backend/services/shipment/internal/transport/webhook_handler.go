package transport

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/shipment/internal/usecase"
)

// maxWebhookBodyBytes bounds how much of a webhook delivery's body this
// service will read, since it's a public, unauthenticated-until-verified
// endpoint.
const maxWebhookBodyBytes = 64 * 1024

// WebhookHandler receives the carrier's interception-decision deliveries.
// It is registered under /api/webhooks, not /api/shipments, and carries no
// JWT auth — a carrier can't present a vendor's bearer token — so every
// trust decision here comes from the use case verifying the payload's
// signature. Mirrors payment/internal/transport/webhook_handler.go.
type WebhookHandler struct {
	shipments *usecase.ShipmentUseCase
	log       zerolog.Logger
}

func NewWebhookHandler(shipments *usecase.ShipmentUseCase, log zerolog.Logger) *WebhookHandler {
	return &WebhookHandler{shipments: shipments, log: log}
}

// Handle reads the raw request body — never a re-parsed/re-encoded version
// of it, since a signature covers the exact bytes sent — and hands it to
// the use case, which verifies the signature and applies the outcome
// idempotently.
func (h *WebhookHandler) Handle(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBodyBytes+1))
	if err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Could not read request body")
		return
	}

	signature := c.GetHeader("X-Mock-Signature")

	if err := h.shipments.ProcessCarrierWebhook(c.Request.Context(), body, signature); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, gin.H{"received": true})
}
