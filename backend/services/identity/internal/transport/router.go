// Package transport wires Identity's HTTP router: middleware, health checks
// and the auth endpoints.
package transport

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/telemetry"
	"shopee/backend/services/identity/internal/usecase"
)

func NewRouter(env string, log zerolog.Logger, jwtManager *authjwt.Manager, authHandler *AuthHandler, adminHandler *AdminHandler, internalHandler *InternalHandler, accessHandler *AccessHandler, adminGuard gin.HandlerFunc, security Security, delivery *usecase.ResetDeliveryUseCase, checkers ...health.Checker) *gin.Engine {
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	// An empty proxy list disables trust in forwarded client IP headers.
	if err := r.SetTrustedProxies(security.TrustedProxies); err != nil {
		panic("invalid trusted proxy configuration")
	}
	r.Use(middleware.RequestID())
	r.Use(telemetry.Middleware())
	r.Use(middleware.StructuredLogging(log))
	r.Use(middleware.Recovery(log))
	r.Use(func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})

	health.RegisterRoutes(r, checkers...)

	requireAuth := middleware.RequireAuth(jwtManager)

	auth := r.Group("/api/auth", security.BrowserProtection(), security.RateLimit())
	{
		auth.POST("/register", authHandler.Register)
		auth.POST("/login", authHandler.Login)
		auth.POST("/refresh", authHandler.Refresh)
		auth.POST("/logout", authHandler.Logout)
		auth.POST("/password-reset/request", authHandler.RequestPasswordReset)
		auth.POST("/password-reset/confirm", authHandler.ConfirmPasswordReset)
		auth.GET("/me", requireAuth, authHandler.Me)
		// AF-19: the caller's admin bundles, and a password re-check that
		// returns a one-time proof for one sensitive operation.
		auth.GET("/permissions", requireAuth, accessHandler.Mine)
		auth.POST("/reauthentications", requireAuth, middleware.RequireRole("admin"), accessHandler.Reauthenticate)
	}

	adminGroup := r.Group("/api/auth/admin", security.BrowserProtection(), requireAuth, middleware.RequireRole("admin"), adminGuard)
	{
		adminGroup.GET("/users", adminHandler.ListUsers)
		adminGroup.PATCH("/users/:id/active", adminHandler.SetActive)
		adminGroup.POST("/users/:id/sessions/:sessionID/revoke", adminHandler.RevokeSession)

		adminGroup.GET("/permission-subjects", accessHandler.Subjects)
		adminGroup.GET("/permission-grants", accessHandler.ListGrants)
		adminGroup.POST("/permission-grants", accessHandler.Grant)
		adminGroup.DELETE("/permission-grants/:id", accessHandler.Revoke)
	}

	internalGroup := r.Group("/internal/users", security.services().Allow(sessionCallers...))
	{
		internalGroup.GET("/:id", internalHandler.GetUser)
	}

	r.POST("/internal/sessions/verify", security.services().Allow(sessionCallers...), authHandler.VerifySession)
	r.POST("/internal/admin-permissions/check", security.services().Allow(sessionCallers...), accessHandler.Check)
	r.POST("/internal/reauth-proofs/consume", security.services().Allow(sessionCallers...), accessHandler.ConsumeProof)
	r.GET("/internal/password-reset-deliveries/:id", serviceKey(security.DeliveryKey, "X-Reset-Delivery-Key"), func(c *gin.Context) {
		message, err := delivery.Message(c.Request.Context(), c.Param("id"))
		if err != nil {
			c.AbortWithStatus(404)
			return
		}
		c.JSON(200, message)
	})
	return r
}
