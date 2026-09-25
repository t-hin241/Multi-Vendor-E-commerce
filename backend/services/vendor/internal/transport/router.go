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
		// A user may own several shops (1:N) — Apply creates a new one every
		// time it's called (both the first shop and any additional one);
		// Mine lists all of them so the frontend can offer a shop switcher.
		vendorGroup.POST("/applications", middleware.RequireRole("vendor"), vendorHandler.Apply)
		vendorGroup.GET("/mine", middleware.RequireRole("vendor"), vendorHandler.Mine)
		vendorGroup.GET("/:vendorId", middleware.RequireRole("vendor"), vendorHandler.Get)
		vendorGroup.PATCH("/:vendorId", middleware.RequireRole("vendor"), vendorHandler.UpdateProfile)
		vendorGroup.POST("/:vendorId/logo", middleware.RequireRole("vendor"), vendorHandler.UploadLogo)
		vendorGroup.POST("/:vendorId/banner", middleware.RequireRole("vendor"), vendorHandler.UploadBanner)

		vendorGroup.POST("/:vendorId/addresses", middleware.RequireRole("vendor"), addressHandler.Add)
		vendorGroup.GET("/:vendorId/addresses", middleware.RequireRole("vendor"), addressHandler.ListMine)
		vendorGroup.PATCH("/:vendorId/addresses/:id", middleware.RequireRole("vendor"), addressHandler.Update)
		vendorGroup.DELETE("/:vendorId/addresses/:id", middleware.RequireRole("vendor"), addressHandler.Delete)
		vendorGroup.PATCH("/:vendorId/addresses/:id/default", middleware.RequireRole("vendor"), addressHandler.SetDefault)

		adminGroup := vendorGroup.Group("/admin", middleware.RequireRole("admin"))
		{
			adminGroup.GET("/applications", adminHandler.ListApplications)
			adminGroup.GET("/applications/:id/audit-log", adminHandler.GetAuditLog)
			adminGroup.PATCH("/applications/:id/approve", adminHandler.Approve)
			adminGroup.PATCH("/applications/:id/reject", adminHandler.Reject)
		}
	}

	// Public shop page: unauthenticated, only ever returns an approved
	// shop's branding (GetPublicProfile 404s anything else). Sibling
	// static-prefix group to /api/vendor/admin above, at the same tree
	// level as the /:vendorId wildcard inside vendorGroup — a shape this
	// router already relies on working correctly.
	r.GET("/api/vendor/public/:vendorId", vendorHandler.GetPublic)

	internalGroup := r.Group("/internal/vendors")
	{
		internalGroup.GET("", internalHandler.ListByIDs)
		internalGroup.GET("/:vendorId/owned-by/:userID", internalHandler.GetOwnedStatus)
	}

	return r
}
