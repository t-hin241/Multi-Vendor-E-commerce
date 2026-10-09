// Package transport wires Inventory's HTTP router: middleware, health
// checks, vendor stock management, and the internal reserve/release
// contract Order uses.
package transport

import (
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"context"
	"net/http"
	"time"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/pkg/telemetry"
)

func NewRouter(
	env string,
	log zerolog.Logger,
	jwtManager *authjwt.Manager,
	itemHandler *ItemHandler,
	internalHandler *InternalHandler,
	adminHandler *AdminHandler,
	adminGuard gin.HandlerFunc,
	internal *serviceauth.Verifier,
	checkers ...health.Checker,
) *gin.Engine {
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(middleware.RequestID())
	// PW-021: audit rows record the shop grant a request used.
	r.Use(shopaccess.RecordGrants())
	r.Use(telemetry.Middleware())
	r.Use(middleware.StructuredLogging(log))
	r.Use(middleware.Recovery(log))
	r.Use(func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		c.Next()
	})

	health.RegisterRoutes(r, checkers...)

	// AF-17: shop members on a buyer or vendor account; each use case
	// checks the shop permission with Vendor.
	vendorGroup := r.Group("/api/inventory", middleware.RequireAuth(jwtManager), middleware.SellerConsole())
	{
		vendorGroup.POST("/items", itemHandler.Create)
		vendorGroup.GET("/items/mine", itemHandler.ListMine)
		vendorGroup.PATCH("/items/:productID/restock", itemHandler.Restock)
		vendorGroup.PATCH("/items/variant/:variantID/restock", itemHandler.RestockVariant)
		vendorGroup.GET("/restock-requests/mine", itemHandler.ListMyRestockRequests)
		vendorGroup.POST("/items/:itemID/stock-counts", itemHandler.RecordStockCount)
	}

	adminGroup := r.Group("/api/inventory/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin"), adminGuard)
	{
		adminGroup.GET("/restock-requests", adminHandler.ListRestockRequests)
		adminGroup.PATCH("/restock-requests/:id/approve", adminHandler.Approve)
		adminGroup.PATCH("/restock-requests/:id/reject", adminHandler.Reject)
	}

	internalGroup := r.Group("/internal/inventory")
	{
		orderOnly := internal.Allow("order")
		stockReaders := internal.Allow("cart", "catalog", "order")
		internalGroup.GET("/operations/:orderID", orderOnly, internalHandler.Operation)
		internalGroup.POST("/reserve", orderOnly, internalHandler.Reserve)
		internalGroup.POST("/release", orderOnly, internalHandler.Release)
		internalGroup.POST("/commit", orderOnly, internalHandler.Commit)
		internalGroup.POST("/returns", orderOnly, internalHandler.RestockReturn)
		internalGroup.POST("/recoveries", orderOnly, internalHandler.RestockRecovery)
		internalGroup.GET("/variants/stock", stockReaders, internalHandler.GetVariantStock)
		internalGroup.GET("/products/:productID/readiness", stockReaders, internalHandler.CheckStockReadiness)
		internalGroup.GET("/products/:productID/stock", stockReaders, internalHandler.GetProductStock)
	}

	return r
}
