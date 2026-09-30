package transport

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/payment/internal/usecase"
)

// maxWebhookBodyBytes bounds how much of a webhook delivery's body this
// service reads; it is public and unauthenticated until verified.
const maxWebhookBodyBytes = 64 * 1024

// RateLimiter counts requests per key.
type RateLimiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

// WebhookHandler receives the payment provider's webhook deliveries. It
// carries no JWT auth; every trust decision comes from the use case
// verifying the payload's signature.
type WebhookHandler struct {
	payments *usecase.PaymentUseCase
	limiter  RateLimiter
	log      zerolog.Logger
}

func NewWebhookHandler(payments *usecase.PaymentUseCase, limiter RateLimiter, log zerolog.Logger) *WebhookHandler {
	return &WebhookHandler{payments: payments, limiter: limiter, log: log}
}

// Handle reads the raw body (a signature covers the exact bytes), verifies
// it, stores the receipt and applies it. 200 means stored: a receipt that
// cannot be matched or is invalid is kept for reconciliation, not retried by
// the provider. 5xx asks the provider to deliver again.
func (h *WebhookHandler) Handle(c *gin.Context) {
	if h.limiter != nil {
		allowed, err := h.limiter.Allow(c.Request.Context(), c.ClientIP())
		if err != nil {
			// Fail open: dropping a payment notification is worse than extra
			// load, and every delivery is verified and deduplicated.
			h.log.Warn().Err(err).Msg("payment_webhook_rate_limit_unavailable")
		} else if !allowed {
			h.log.Warn().Str("client_ip", c.ClientIP()).Msg("payment_webhook_rate_limited")
			httpresponse.Error(c, http.StatusTooManyRequests, "rate_limited", "Too many requests")
			return
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBodyBytes+1))
	if err != nil || len(body) > maxWebhookBodyBytes {
		httpresponse.Error(c, http.StatusRequestEntityTooLarge, "validation_error", "Webhook body too large or unreadable")
		return
	}
	if err := h.payments.ProcessWebhook(ctx, body, c.GetHeader("X-Mock-Signature")); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"received": true})
}
