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
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
)

func NewRouter(
	env string,
	log zerolog.Logger,
	jwtManager *authjwt.Manager,
	vendorHandler *VendorHandler,
	addressHandler *VendorAddressHandler,
	adminHandler *AdminHandler,
	internalHandler *InternalHandler,
	policyHandler *PolicyHandler,
	staffHandler *StaffHandler,
	adminGuard gin.HandlerFunc,
	internal *serviceauth.Verifier,
	checkers ...health.Checker,
) *gin.Engine {
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(telemetry.Middleware())
	r.Use(middleware.StructuredLogging(log))
	r.Use(middleware.Recovery(log))
	r.Use(requestBounds())

	health.RegisterRoutes(r, checkers...)

	requireAuth := middleware.RequireAuth(jwtManager)

	vendorGroup := r.Group("/api/vendor", requireAuth)
	{
		// A user may own several shops (1:N) — Apply creates a new one every
		// time it's called (both the first shop and any additional one);
		// Mine lists all of them so the frontend can offer a shop switcher.
		vendorGroup.POST("/applications", middleware.RequireRole("vendor"), vendorHandler.Apply)
		vendorGroup.GET("/mine", middleware.RequireRole("vendor"), vendorHandler.Mine)
		// AF-17: any active member (an owner, or staff on a buyer or vendor
		// account) opens the shop; the membership decides, not the role.
		member := middleware.RequireRole("vendor", "buyer")
		vendorGroup.GET("/accessible-shops", member, staffHandler.AccessibleShops)
		vendorGroup.GET("/staff-permissions", member, staffHandler.Permissions)
		vendorGroup.POST("/staff-invitations/accept", member, staffHandler.Accept)
		vendorGroup.GET("/:vendorId", member, staffHandler.Shop)
		vendorGroup.POST("/:vendorId/staff-invitations", member, staffHandler.Invite)
		vendorGroup.GET("/:vendorId/staff-invitations", member, staffHandler.ListInvitations)
		vendorGroup.DELETE("/:vendorId/staff-invitations/:id", member, staffHandler.RevokeInvitation)
		vendorGroup.GET("/:vendorId/members", member, staffHandler.ListMembers)
		vendorGroup.GET("/:vendorId/members/:userID", member, staffHandler.GetMember)
		vendorGroup.PATCH("/:vendorId/members/:userID", member, staffHandler.UpdateMember)
		vendorGroup.DELETE("/:vendorId/members/:userID", member, staffHandler.RemoveMember)
		vendorGroup.PATCH("/:vendorId", middleware.RequireRole("vendor"), vendorHandler.UpdateProfile)
		vendorGroup.POST("/:vendorId/resubmit", middleware.RequireRole("vendor"), vendorHandler.Resubmit)
		vendorGroup.POST("/:vendorId/logo", middleware.RequireRole("vendor"), vendorHandler.UploadLogo)
		vendorGroup.POST("/:vendorId/banner", middleware.RequireRole("vendor"), vendorHandler.UploadBanner)

		vendorGroup.POST("/:vendorId/addresses", middleware.RequireRole("vendor"), addressHandler.Add)
		vendorGroup.GET("/:vendorId/addresses", middleware.RequireRole("vendor"), addressHandler.ListMine)
		vendorGroup.PATCH("/:vendorId/addresses/:id", middleware.RequireRole("vendor"), addressHandler.Update)
		vendorGroup.DELETE("/:vendorId/addresses/:id", middleware.RequireRole("vendor"), addressHandler.Delete)
		vendorGroup.PATCH("/:vendorId/addresses/:id/default", middleware.RequireRole("vendor"), addressHandler.SetDefault)

		vendorGroup.POST("/:vendorId/policy-proposals", middleware.RequireRole("vendor"), policyHandler.Propose)
		vendorGroup.GET("/:vendorId/policy-proposals", middleware.RequireRole("vendor"), policyHandler.ListMine)

		adminGroup := vendorGroup.Group("/admin", middleware.RequireRole("admin"), adminGuard)
		{
			adminGroup.GET("/operations", adminHandler.Operations)
			adminGroup.GET("/applications", adminHandler.ListApplications)
			adminGroup.GET("/applications/:id/audit-log", adminHandler.GetAuditLog)
			adminGroup.PATCH("/applications/:id/suspend", adminHandler.Suspend)
			adminGroup.PATCH("/applications/:id/restore", adminHandler.Restore)
			adminGroup.POST("/applications/:id/replay", adminHandler.Replay)
			adminGroup.PATCH("/applications/:id/approve", adminHandler.Approve)
			adminGroup.PATCH("/applications/:id/reject", adminHandler.Reject)

			adminGroup.GET("/policy-versions", policyHandler.AdminList)
			adminGroup.POST("/policy-versions", policyHandler.CreateDraft)
			adminGroup.POST("/policy-versions/:id/publications", policyHandler.Publish)
			adminGroup.POST("/policy-versions/:id/withdrawal", policyHandler.Withdraw)
			adminGroup.GET("/policy-proposals", policyHandler.AdminProposals)
			adminGroup.POST("/policy-proposals/:id/decisions", policyHandler.Decide)
		}
	}

	// Public shop page: unauthenticated, only ever returns an approved
	// shop's branding (GetPublicProfile 404s anything else). Sibling
	// static-prefix group to /api/vendor/admin above, at the same tree
	// level as the /:vendorId wildcard inside vendorGroup — a shape this
	// router already relies on working correctly.
	r.GET("/api/vendor/public/:vendorId", policyHandler.PublicShop)
	// AF-02: marketplace policies in force, their history and any published
	// version (links kept on orders stay readable).
	r.GET("/api/vendor/public/policies", policyHandler.Public)
	r.GET("/api/vendor/public/policies/:kind/versions", policyHandler.History)
	r.GET("/api/vendor/public/policies/:kind/versions/:version", policyHandler.Version)

	internalGroup := r.Group("/internal/vendors")
	{
		internalGroup.GET("", internal.Allow("catalog"), internalHandler.ListByIDs)
		internalGroup.GET("/sale-status", internal.Allow("catalog", "order"), internalHandler.SaleStatus)
		// AF-17 authorize contract; owned-by stays owner-only for callers
		// not migrated yet, so they can never grant staff access.
		internalGroup.POST("/authorize", internal.Allow("catalog", "inventory", "order", "review", "shipment", "payment"), staffHandler.Authorize)
		internalGroup.GET("/:vendorId/owned-by/:userID", internal.Allow("catalog", "inventory", "order", "review", "shipment", "payment"), internalHandler.GetOwnedStatus)
	}

	return r
}
