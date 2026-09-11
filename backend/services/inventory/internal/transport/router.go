// Package transport wires Inventory's HTTP router: middleware, health
// checks, vendor stock management, and the internal reserve/release
// contract Order uses.
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
	itemHandler *ItemHandler,
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

	vendorGroup := r.Group("/api/inventory", middleware.RequireAuth(jwtManager), middleware.RequireRole("vendor"))
	{
		vendorGroup.POST("/items", itemHandler.Create)
		vendorGroup.GET("/items/mine", itemHandler.ListMine)
		vendorGroup.PATCH("/items/:productID/restock", itemHandler.Restock)
		vendorGroup.PATCH("/items/variant/:variantID/restock", itemHandler.RestockVariant)
	}

	internalGroup := r.Group("/internal/inventory")
	{
		internalGroup.POST("/reserve", internalHandler.Reserve)
		internalGroup.POST("/release", internalHandler.Release)
		internalGroup.POST("/commit", internalHandler.Commit)
		internalGroup.GET("/variants/stock", internalHandler.GetVariantStock)
	}

	return r
}
