// Package transport wires Notification's HTTP router: middleware, health
// checks, the service-authenticated notify contract, and admin's delivery
// view with its audited retry.
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

func NewRouter(env string, log zerolog.Logger, jwtManager *authjwt.Manager, serviceKey string, internalHandler *InternalHandler, adminHandler *AdminHandler, checkers ...health.Checker) *gin.Engine {
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.StructuredLogging(log))
	r.Use(middleware.Recovery(log))
	r.Use(func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
		c.Next()
	})

	health.RegisterRoutes(r, checkers...)

	// Only services holding the internal key may queue a notification.
	r.POST("/internal/notifications", serviceauth.Require(serviceKey, serviceauth.Header), internalHandler.Notify)

	adminGroup := r.Group("/api/notifications/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin"), func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Next()
	})
	{
		adminGroup.GET("", adminHandler.List)
		adminGroup.GET("/operations", adminHandler.Operations)
		adminGroup.GET("/:id/attempts", adminHandler.Attempts)
		adminGroup.POST("/:id/retry", adminHandler.Retry)
	}

	return r
}
