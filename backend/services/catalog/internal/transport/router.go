// Package transport wires Catalog's HTTP router: middleware, health checks,
// the public storefront, vendor product management, image upload, category
// management and admin moderation.
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
	categoryHandler *CategoryHandler,
	productHandler *ProductHandler,
	storefrontHandler *StorefrontHandler,
	adminHandler *AdminHandler,
	internalHandler *InternalHandler,
	attributeHandler *AttributeHandler,
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

	// Categories: public read, admin write.
	r.GET("/api/catalog/categories", categoryHandler.List)
	r.POST("/api/catalog/categories", requireAuth, middleware.RequireRole("admin"), categoryHandler.Create)

	// Attribute template: public — describes which fields apply to a
	// category, same trust level as GET /categories.
	r.GET("/api/catalog/categories/:id/attribute-template", attributeHandler.GetTemplate)

	// Attribute catalog + category attribute rules: admin-authored.
	attributeAdminGroup := r.Group("/api/catalog", requireAuth, middleware.RequireRole("admin"))
	{
		attributeAdminGroup.GET("/attributes", attributeHandler.List)
		attributeAdminGroup.POST("/attributes", attributeHandler.Create)
		attributeAdminGroup.POST("/attributes/:id/options", attributeHandler.AddOption)
		attributeAdminGroup.POST("/categories/:id/attribute-rules", attributeHandler.SetCategoryRule)
	}

	// Storefront: public, only ever returns approved+active products.
	// Uses ":id" (not ":slug") as the wildcard name even though the value is
	// a slug: Gin requires one consistent wildcard name per position within
	// a method's route tree, and the vendor GET /:id/media route below sits
	// at the same position in the GET tree.
	r.GET("/api/catalog/products", storefrontHandler.List)
	// OptionalAuth: still fully public, but lets GetBySlug show exact stock
	// to a recognized admin or the product's own vendor (see GetPublicBySlug).
	r.GET("/api/catalog/products/:id", middleware.OptionalAuth(jwtManager), storefrontHandler.GetBySlug)

	// Vendor product management.
	vendorGroup := r.Group("/api/catalog/products", requireAuth, middleware.RequireRole("vendor"))
	{
		vendorGroup.POST("", productHandler.Create)
		vendorGroup.GET("/mine", productHandler.ListMine)
		vendorGroup.PATCH("/:id/submit", productHandler.Submit)
		vendorGroup.PATCH("/:id/active", productHandler.SetActive)
		vendorGroup.POST("/:id/images", productHandler.UploadImage)
		vendorGroup.GET("/:id/images", productHandler.ListImages)
		vendorGroup.DELETE("/:id/images", productHandler.DeleteImage)
		vendorGroup.POST("/:id/media", productHandler.UploadMedia)
		vendorGroup.GET("/:id/media", productHandler.ListMedia)
		vendorGroup.POST("/:id/variants", productHandler.CreateVariant)
		vendorGroup.GET("/:id/variants", productHandler.ListVariants)
	}

	// Admin moderation.
	adminGroup := r.Group("/api/catalog/products/admin", requireAuth, middleware.RequireRole("admin"))
	{
		adminGroup.GET("", adminHandler.ListForModeration)
		adminGroup.GET("/:id", adminHandler.GetForModeration)
		adminGroup.GET("/:id/audit-log", adminHandler.GetAuditLog)
		adminGroup.PATCH("/:id/approve", adminHandler.Approve)
		adminGroup.PATCH("/:id/reject", adminHandler.Reject)
	}

	r.GET("/internal/products/:id", internalHandler.GetByID)
	r.GET("/internal/products/variants/:variantId", internalHandler.GetVariantOwner)

	return r
}
