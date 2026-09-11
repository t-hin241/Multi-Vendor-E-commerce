// Package transport wires Order's HTTP router: middleware, health checks,
// checkout, the buyer's own orders, and the vendor's own sub-orders.
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
	orderHandler *OrderHandler,
	addressHandler *BuyerAddressHandler,
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

	buyerGroup := r.Group("/api/orders", requireAuth, middleware.RequireRole("buyer"))
	{
		buyerGroup.POST("/checkout", orderHandler.Checkout)
		buyerGroup.GET("/mine", orderHandler.ListMine)
		buyerGroup.GET("/:id", orderHandler.Get)
		buyerGroup.POST("/:id/cancel", orderHandler.Cancel)

		buyerGroup.POST("/addresses", addressHandler.Add)
		buyerGroup.GET("/addresses", addressHandler.ListMine)
		buyerGroup.PATCH("/addresses/:id", addressHandler.Update)
		buyerGroup.DELETE("/addresses/:id", addressHandler.Delete)
		buyerGroup.PATCH("/addresses/:id/default", addressHandler.SetDefault)
	}

	vendorGroup := r.Group("/api/orders/vendor", requireAuth, middleware.RequireRole("vendor"))
	{
		vendorGroup.GET("/mine", orderHandler.ListVendorMine)
		vendorGroup.GET("/summary", orderHandler.Summary)
		vendorGroup.GET("/export.csv", orderHandler.ExportCSV)
		vendorGroup.PATCH("/:vendorOrderID/status", orderHandler.UpdateVendorOrderStatus)
	}

	adminGroup := r.Group("/api/orders/admin", requireAuth, middleware.RequireRole("admin"))
	{
		adminGroup.GET("", adminHandler.List)
		adminGroup.POST("/:id/transition", adminHandler.Transition)
		adminGroup.GET("/commission-rules", adminHandler.ListCommissionRules)
		adminGroup.POST("/commission-rules", adminHandler.SetCommissionRule)
	}

	internalGroup := r.Group("/internal/orders")
	{
		internalGroup.GET("/:id", internalHandler.Get)
		internalGroup.POST("/:id/mark-paid", internalHandler.MarkPaid)
		internalGroup.POST("/:id/mark-payment-failed", internalHandler.MarkPaymentFailed)
		internalGroup.GET("/products/quantity-sold", internalHandler.QuantitySoldByProductIDs)
	}

	internalVendorOrderGroup := r.Group("/internal/vendor-orders")
	{
		internalVendorOrderGroup.GET("/:id", internalHandler.GetVendorOrder)
	}

	return r
}
