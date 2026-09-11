// Package transport wires Vendor's HTTP router: middleware, health checks,
// the buyer/vendor-facing endpoints, admin moderation and the internal
// vendor-status lookup Catalog uses.
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
	vendorHandler *VendorHandler,
	addressHandler *VendorAddressHandler,
	adminHandler *AdminHandler,
	internalHandler *InternalHandler,
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

	requireAuth := middleware.RequireAuth(jwtManager)

	vendorGroup := r.Group("/api/vendor", requireAuth)
	{
		vendorGroup.POST("/applications", middleware.RequireRole("vendor"), vendorHandler.Apply)
		vendorGroup.GET("/me", middleware.RequireRole("vendor"), vendorHandler.Me)
		vendorGroup.PATCH("/me", middleware.RequireRole("vendor"), vendorHandler.UpdateProfile)

		vendorGroup.POST("/addresses", middleware.RequireRole("vendor"), addressHandler.Add)
		vendorGroup.GET("/addresses", middleware.RequireRole("vendor"), addressHandler.ListMine)
		vendorGroup.PATCH("/addresses/:id", middleware.RequireRole("vendor"), addressHandler.Update)
		vendorGroup.DELETE("/addresses/:id", middleware.RequireRole("vendor"), addressHandler.Delete)
		vendorGroup.PATCH("/addresses/:id/default", middleware.RequireRole("vendor"), addressHandler.SetDefault)

		adminGroup := vendorGroup.Group("/admin", middleware.RequireRole("admin"))
		{
			adminGroup.GET("/applications", adminHandler.ListApplications)
			adminGroup.PATCH("/applications/:id/approve", adminHandler.Approve)
			adminGroup.PATCH("/applications/:id/reject", adminHandler.Reject)
		}
	}

	internalGroup := r.Group("/internal/vendors")
	{
		internalGroup.GET("", internalHandler.ListByIDs)
		internalGroup.GET("/by-user/:userID", internalHandler.GetStatusByUserID)
	}

	return r
}
