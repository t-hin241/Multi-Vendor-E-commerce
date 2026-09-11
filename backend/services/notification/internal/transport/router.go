// Package transport wires Notification's HTTP router: middleware, health
// checks, the internal notify contract, and admin's read-only view of what
// was sent.
package transport

import (
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/middleware"
)

func NewRouter(env string, log zerolog.Logger, jwtManager *authjwt.Manager, internalHandler *InternalHandler, adminHandler *AdminHandler, checkers ...health.Checker) *gin.Engine {
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.StructuredLogging(log))
	r.Use(middleware.Recovery(log))

	health.RegisterRoutes(r, checkers...)

	internalGroup := r.Group("/internal/notifications")
	{
		internalGroup.POST("", internalHandler.Notify)
	}

	adminGroup := r.Group("/api/notifications/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin"))
	{
		adminGroup.GET("", adminHandler.List)
	}

	return r
}
