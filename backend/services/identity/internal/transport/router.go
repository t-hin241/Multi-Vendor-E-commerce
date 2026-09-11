// Package transport wires Identity's HTTP router: middleware, health checks
// and the auth endpoints.
package transport

import (
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/middleware"
)

func NewRouter(env string, log zerolog.Logger, jwtManager *authjwt.Manager, authHandler *AuthHandler, internalHandler *InternalHandler, checkers ...health.Checker) *gin.Engine {
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.StructuredLogging(log))
	r.Use(middleware.Recovery(log))

	health.RegisterRoutes(r, checkers...)

	auth := r.Group("/api/auth")
	{
		auth.POST("/register", authHandler.Register)
		auth.POST("/login", authHandler.Login)
		auth.POST("/refresh", authHandler.Refresh)
		auth.POST("/logout", authHandler.Logout)
		auth.POST("/password-reset/request", authHandler.RequestPasswordReset)
		auth.POST("/password-reset/confirm", authHandler.ConfirmPasswordReset)
		auth.GET("/me", middleware.RequireAuth(jwtManager), authHandler.Me)
	}

	internalGroup := r.Group("/internal/users")
	{
		internalGroup.GET("/:id", internalHandler.GetUser)
	}

	return r
}
