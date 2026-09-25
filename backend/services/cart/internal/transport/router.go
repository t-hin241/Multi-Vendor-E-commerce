// Package transport wires Cart's HTTP router: middleware, health checks,
// and the buyer-only cart endpoints.
package transport

import (
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/middleware"
)

func NewRouter(env string, log zerolog.Logger, jwtManager *authjwt.Manager, cartHandler *CartHandler, checkers ...health.Checker) *gin.Engine {
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.StructuredLogging(log))
	r.Use(middleware.Recovery(log))

	health.RegisterRoutes(r, checkers...)

	cartGroup := r.Group("/api/cart", middleware.RequireAuth(jwtManager), middleware.RequireRole("buyer"))
	{
		cartGroup.GET("", cartHandler.View)
		cartGroup.DELETE("", cartHandler.Clear)
		cartGroup.POST("/items", cartHandler.AddItem)
		cartGroup.PATCH("/items/:productID", cartHandler.SetItemQuantity)
		cartGroup.DELETE("/items/:productID", cartHandler.RemoveItem)
	}

	return r
}
