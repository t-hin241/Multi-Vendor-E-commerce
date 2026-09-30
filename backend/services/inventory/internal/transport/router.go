// Package transport wires Inventory's HTTP router: middleware, health
// checks, vendor stock management, and the internal reserve/release
// contract Order uses.
package transport

import (
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"context"
	"net/http"
	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/serviceauth"
	"time"
)

func NewRouter(
	env string,
	log zerolog.Logger,
	jwtManager *authjwt.Manager,
	itemHandler *ItemHandler,
	internalHandler *InternalHandler,
	adminHandler *AdminHandler,
	internalKey string,
	checkers ...health.Checker,
) *gin.Engine {
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
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		c.Next()
	})

	health.RegisterRoutes(r, checkers...)

	vendorGroup := r.Group("/api/inventory", middleware.RequireAuth(jwtManager), middleware.RequireRole("vendor"))
	{
		vendorGroup.POST("/items", itemHandler.Create)
		vendorGroup.GET("/items/mine", itemHandler.ListMine)
		vendorGroup.PATCH("/items/:productID/restock", itemHandler.Restock)
		vendorGroup.PATCH("/items/variant/:variantID/restock", itemHandler.RestockVariant)
		vendorGroup.GET("/restock-requests/mine", itemHandler.ListMyRestockRequests)
		vendorGroup.POST("/items/:itemID/stock-counts", itemHandler.RecordStockCount)
	}

	adminGroup := r.Group("/api/inventory/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin"))
	{
		adminGroup.GET("/restock-requests", adminHandler.ListRestockRequests)
		adminGroup.PATCH("/restock-requests/:id/approve", adminHandler.Approve)
		adminGroup.PATCH("/restock-requests/:id/reject", adminHandler.Reject)
	}

	internalGroup := r.Group("/internal/inventory", serviceauth.Require(internalKey, serviceauth.Header))
	{
		internalGroup.GET("/operations/:orderID", internalHandler.Operation)
		internalGroup.POST("/reserve", internalHandler.Reserve)
		internalGroup.POST("/release", internalHandler.Release)
		internalGroup.POST("/commit", internalHandler.Commit)
		internalGroup.GET("/variants/stock", internalHandler.GetVariantStock)
		internalGroup.GET("/products/:productID/readiness", internalHandler.CheckStockReadiness)
		internalGroup.GET("/products/:productID/stock", internalHandler.GetProductStock)
	}

	return r
}
