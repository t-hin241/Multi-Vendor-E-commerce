// Package transport wires Shipment's HTTP router: middleware, health
// checks, the vendor-facing shipment/tracking/shipping-method endpoints,
// the buyer-facing read-only shipment views, admin carrier/zone/fee-rule
// management, and the internal create/cancel contract Order calls.
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
	shipmentHandler *ShipmentHandler,
	adminHandler *AdminHandler,
	vendorMethodHandler *VendorShippingMethodHandler,
	internalHandler *InternalHandler,
	webhookHandler *WebhookHandler,
	opsHandler *OpsHandler,
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
	r.Use(boundRequest())

	health.RegisterRoutes(r, checkers...)

	requireAuth := middleware.RequireAuth(jwtManager)

	// Public: a vendor (or anyone) can see which carriers admin has turned
	// on, same trust level as Catalog's public GET /api/catalog/categories.
	r.GET("/api/shipments/carriers", adminHandler.ListActiveCarriers)

	// Shared between vendor and buyer: both can list "their own" shipments
	// and read one shipment's tracking events — the handler/usecase dispatch
	// on the caller's own role/ownership, since Gin only allows one handler
	// per method+path and these two roles need the identical paths.
	sharedGroup := r.Group("/api/shipments", requireAuth, middleware.RequireRole("vendor", "buyer"))
	{
		sharedGroup.GET("/mine", shipmentHandler.ListMine)
		sharedGroup.GET("/:id/events", shipmentHandler.ListEvents)
	}

	vendorGroup := r.Group("/api/shipments", requireAuth, middleware.RequireRole("vendor"))
	{
		vendorGroup.POST("", shipmentHandler.Create)
		vendorGroup.GET("/by-vendor-order/:vendorOrderID", shipmentHandler.GetByVendorOrderID)
		vendorGroup.PATCH("/:id/advance", shipmentHandler.Advance)
		vendorGroup.POST("/:id/ready", shipmentHandler.MarkReady)
		vendorGroup.POST("/:id/ship", shipmentHandler.MarkShipped)
		vendorGroup.POST("/:id/tracking", shipmentHandler.UpdateTracking)
		vendorGroup.POST("/:id/failed-attempts", shipmentHandler.FailedAttempt)
		vendorGroup.POST("/:id/deliver", shipmentHandler.MarkDelivered)
		vendorGroup.POST("/:id/return", shipmentHandler.MarkReturned)
		vendorGroup.POST("/:id/interception-decision", shipmentHandler.ResolveInterception)
		vendorGroup.POST("/:id/simulate-carrier-decision", shipmentHandler.SimulateCarrierDecision)
	}

	vendorMethodGroup := r.Group("/api/shipments/vendor/methods", requireAuth, middleware.RequireRole("vendor"))
	{
		vendorMethodGroup.POST("", vendorMethodHandler.Enable)
		vendorMethodGroup.GET("", vendorMethodHandler.ListMine)
		vendorMethodGroup.PATCH("/:id/default", vendorMethodHandler.SetDefault)
		vendorMethodGroup.PATCH("/:id/active", vendorMethodHandler.SetActive)
	}

	adminGroup := r.Group("/api/shipments/admin", requireAuth, middleware.RequireRole("admin"))
	{
		adminGroup.POST("/carriers", adminHandler.CreateCarrier)
		adminGroup.GET("/carriers", adminHandler.ListCarriers)
		adminGroup.PATCH("/carriers/:id/active", adminHandler.SetCarrierActive)
		adminGroup.POST("/zones", adminHandler.CreateZone)
		adminGroup.GET("/zones", adminHandler.ListZones)
		adminGroup.POST("/zones/:id/provinces", adminHandler.AddProvinceToZone)
		adminGroup.GET("/zones/:id/provinces", adminHandler.ListZoneProvinces)
		adminGroup.POST("/fee-rules", adminHandler.SetFeeRule)
		adminGroup.GET("/fee-rules", adminHandler.ListFeeRules)

		adminGroup.GET("/operations", opsHandler.Operations)
		adminGroup.GET("/shipments/:id", opsHandler.Get)
		adminGroup.POST("/shipments/:id/deliver", opsHandler.MarkDelivered)
		adminGroup.POST("/shipments/:id/failed-attempts", opsHandler.FailedAttempt)
		adminGroup.POST("/shipments/:id/return", opsHandler.MarkReturned)
		adminGroup.POST("/shipments/:id/tracking", opsHandler.UpdateTracking)
		adminGroup.POST("/shipments/:id/interception-decision", opsHandler.ResolveInterception)
		adminGroup.POST("/order-events/:id/retry", opsHandler.RetryOrderEvent)
	}

	internalGroup := r.Group("/internal/shipments", serviceauth.Require(internalKey, serviceauth.Header))
	{
		internalGroup.POST("", internalHandler.CreateShipment)
		internalGroup.POST("/quotes", internalHandler.Quote)
		internalGroup.POST("/by-vendor-order/:id/cancel", internalHandler.CancelForVendorOrder)
	}

	// The carrier calls this directly, with no bearer token — its signature
	// is the only trust boundary, verified inside the handler.
	r.POST("/api/webhooks/shipment-carrier", webhookHandler.Handle)

	return r
}

// boundRequest caps processing time and body size.
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
