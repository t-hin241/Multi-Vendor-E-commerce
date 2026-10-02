// Package transport wires Cart's HTTP router: middleware, health checks,
// the buyer-only cart endpoints, and the service-authenticated checkout
// contract Order uses.
package transport

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/serviceauth"
)

const (
	requestTimeout = 15 * time.Second
	maxBodyBytes   = 64 << 10
)

func NewRouter(env string, log zerolog.Logger, jwtManager *authjwt.Manager, cartHandler *CartHandler, internalHandler *InternalHandler, internal *serviceauth.Verifier, checkers ...health.Checker) *gin.Engine {
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.StructuredLogging(log))
	r.Use(middleware.Recovery(log))
	r.Use(boundRequest())

	health.RegisterRoutes(r, checkers...)

	cartGroup := r.Group("/api/cart", middleware.RequireAuth(jwtManager), middleware.RequireRole("buyer"))
	{
		cartGroup.GET("", cartHandler.View)
		cartGroup.DELETE("", cartHandler.Clear)
		cartGroup.POST("/items", cartHandler.AddItem)
		cartGroup.PATCH("/items/:productID", cartHandler.SetItemQuantity)
		cartGroup.DELETE("/items/:productID", cartHandler.RemoveItem)
		cartGroup.POST("/price-confirmations", cartHandler.ConfirmPrices)
	}

	internalGroup := r.Group("/internal/carts", internal.Allow("order"))
	{
		internalGroup.GET("/:buyerId/lines", internalHandler.Lines)
		internalGroup.POST("/:buyerId/checkout-snapshots", internalHandler.CreateSnapshot)
		internalGroup.POST("/:buyerId/checkout-snapshots/:operationId/consume", internalHandler.Consume)
	}

	return r
}

// boundRequest caps every request's body size and total processing time.
func boundRequest() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), requestTimeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		}
		c.Next()
	}
}
