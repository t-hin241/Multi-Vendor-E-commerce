// Package transport wires Order's HTTP router: middleware, health checks,
// buyer checkout/orders/returns, the vendor's own sub-orders and returns,
// admin operations, and the service-authenticated internal contracts.
package transport

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/serviceauth"
)

func NewRouter(
	env string,
	log zerolog.Logger,
	jwtManager *authjwt.Manager,
	orderHandler *OrderHandler,
	addressHandler *BuyerAddressHandler,
	adminHandler *AdminHandler,
	internalHandler *InternalHandler,
	returnHandler *ReturnHandler,
	internal *serviceauth.Verifier,
	checkers ...health.Checker,
) *gin.Engine {
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.StructuredLogging(log))
	r.Use(middleware.Recovery(log))
	r.Use(boundRequest())

	health.RegisterRoutes(r, checkers...)

	requireAuth := middleware.RequireAuth(jwtManager)

	buyerGroup := r.Group("/api/orders", requireAuth, middleware.RequireRole("buyer"))
	{
		buyerGroup.POST("/checkout", orderHandler.Checkout)
		buyerGroup.POST("/checkout/preview", orderHandler.Preview)
		buyerGroup.GET("/mine", orderHandler.ListMine)
		buyerGroup.GET("/:id", orderHandler.Get)
		buyerGroup.POST("/:id/cancel", orderHandler.Cancel)
		buyerGroup.POST("/:id/return-requests", returnHandler.Create)
		buyerGroup.GET("/return-requests/mine", returnHandler.ListMine)

		buyerGroup.POST("/addresses", addressHandler.Add)
		buyerGroup.GET("/addresses", addressHandler.ListMine)
		buyerGroup.PATCH("/addresses/:id", addressHandler.Update)
		buyerGroup.DELETE("/addresses/:id", addressHandler.Delete)
		buyerGroup.PATCH("/addresses/:id/default", addressHandler.SetDefault)
	}

	vendorGroup := r.Group("/api/orders/vendor", requireAuth, middleware.RequireRole("vendor"))
	{
		vendorGroup.GET("/return-requests", returnHandler.VendorList)
		vendorGroup.POST("/return-requests/:id/confirm", returnHandler.ConfirmByVendor)
		vendorGroup.POST("/return-requests/:id/receive", returnHandler.Receive)
		vendorGroup.GET("/mine", orderHandler.ListVendorMine)
		vendorGroup.GET("/summary", orderHandler.Summary)
		vendorGroup.GET("/export.csv", orderHandler.ExportCSV)
		vendorGroup.PATCH("/:vendorOrderID/status", orderHandler.UpdateVendorOrderStatus)
	}

	adminGroup := r.Group("/api/orders/admin", requireAuth, middleware.RequireRole("admin"))
	{
		adminGroup.GET("", adminHandler.List)
		adminGroup.GET("/refunds", adminHandler.ListRefunds)
		adminGroup.GET("/payment-exceptions", adminHandler.PaymentExceptions)
		adminGroup.GET("/operations", adminHandler.Operations)
		adminGroup.POST("/operations/effects/:effectID/replay", adminHandler.ReplayEffect)
		adminGroup.GET("/return-requests", returnHandler.AdminList)
		adminGroup.GET("/return-requests/:id/history", returnHandler.History)
		adminGroup.POST("/return-requests/:id/decision", returnHandler.Decide)
		adminGroup.POST("/return-requests/:id/receive", returnHandler.Receive)
		adminGroup.POST("/return-requests/:id/retry-refund", returnHandler.RetryRefund)
		adminGroup.GET("/commission-rules", adminHandler.ListCommissionRules)
		adminGroup.POST("/commission-rules", adminHandler.SetCommissionRule)
		adminGroup.GET("/:id", adminHandler.Get)
		adminGroup.POST("/:id/transition", adminHandler.Transition)
		adminGroup.POST("/:id/refunds", adminHandler.CreateRefund)
	}

	// Every internal route requires a service identity and names the
	// services allowed to call it: they expose buyer addresses, totals and
	// payment transitions (PLT-01, least privilege).
	internalGroup := r.Group("/internal")
	{
		payment := internal.Allow("payment")
		internalGroup.GET("/orders/:id", payment, internalHandler.Get)
		internalGroup.POST("/orders/:id/mark-paid", payment, internalHandler.MarkPaid)
		internalGroup.POST("/orders/:id/mark-payment-failed", payment, internalHandler.MarkPaymentFailed)
		internalGroup.GET("/orders/:id/inventory-status", internal.Allow("inventory"), internalHandler.InventoryStatus)
		internalGroup.GET("/orders/products/quantity-sold", internal.Allow("catalog"), internalHandler.QuantitySoldByProductIDs)
		internalGroup.GET("/orders/review-eligibility", internal.Allow("review"), internalHandler.ListReviewEligibility)
		internalGroup.GET("/vendor-orders/:id", internal.Allow("shipment"), internalHandler.GetVendorOrder)
		internalGroup.POST("/inventory-events", internal.Allow("inventory"), internalHandler.InventoryEvent)
		internalGroup.POST("/refund-events", payment, internalHandler.RefundEvent)
		internalGroup.POST("/settlements/holds", payment, internalHandler.SettlementHolds)
		internalGroup.POST("/shipment-events", internal.Allow("shipment"), internalHandler.ShipmentEvent)
	}

	return r
}

// boundRequest caps request body size and processing time.
func boundRequest() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		}
		c.Next()
	}
}
