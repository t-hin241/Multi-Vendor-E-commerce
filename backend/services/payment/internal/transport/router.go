// Package transport wires Payment's HTTP router: middleware, health checks,
// the buyer-facing payment intent flow, and the public webhook endpoint.
package transport

import (
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/middleware"
)

func NewRouter(
	env string,
	log zerolog.Logger,
	jwtManager *authjwt.Manager,
	paymentHandler *PaymentHandler,
	webhookHandler *WebhookHandler,
	checkers ...health.Checker,
) *gin.Engine {
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.StructuredLogging(log))
	r.Use(middleware.Recovery(log))

	health.RegisterRoutes(r, checkers...)

	buyerGroup := r.Group("/api/payments", middleware.RequireAuth(jwtManager), middleware.RequireRole("buyer"))
	{
		buyerGroup.POST("/intents", paymentHandler.CreateIntent)
		buyerGroup.GET("/intents/:id", paymentHandler.Get)
		buyerGroup.POST("/intents/:id/simulate", paymentHandler.Simulate)
	}

	// The provider calls this directly, with no bearer token — its
	// signature is the only trust boundary, verified inside the handler.
	r.POST("/api/webhooks/payments", webhookHandler.Handle)

	return r
}
