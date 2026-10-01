package transport

import (
	"context"
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/services/notification/internal/sender"
	"shopee/backend/services/notification/internal/usecase"
)

// RegisterPasswordReset serves Identity's reset delivery call. A failure is
// logged with the delivery id and a short reason only (never the address
// or the link); Identity retries until the reset expires.
func RegisterPasswordReset(r *gin.Engine, key string, uc *usecase.PasswordResetUseCase, log zerolog.Logger) {
	r.POST("/internal/notifications/password-reset", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(c.GetHeader("X-Reset-Delivery-Key"))) != 1 {
			c.AbortWithStatus(403)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
		var input struct {
			ID string `json:"delivery_id"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.AbortWithStatus(400)
			return
		}
		if _, err := uuid.Parse(input.ID); err != nil {
			c.AbortWithStatus(400)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		if err := uc.Deliver(ctx, input.ID); err != nil {
			log.Warn().Str("delivery_id", input.ID).Str("reason", sender.Classify(err).Reason).Msg("password_reset_delivery_failed")
			c.AbortWithStatus(503)
			return
		}
		c.Status(204)
	})
}
