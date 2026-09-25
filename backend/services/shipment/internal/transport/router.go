// Package transport wires Shipment's HTTP router: middleware, health
// checks, the vendor-facing shipment/tracking/shipping-method endpoints,
// the buyer-facing read-only shipment views, admin carrier/zone/fee-rule
// management, and the internal create/cancel contract Order calls.
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
	shipmentHandler *ShipmentHandler,
	adminHandler *AdminHandler,
	vendorMethodHandler *VendorShippingMethodHandler,
	internalHandler *InternalHandler,
	webhookHandler *WebhookHandler,
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
	}

	internalGroup := r.Group("/internal/shipments")
	{
		internalGroup.POST("", internalHandler.CreateShipment)
		internalGroup.POST("/by-vendor-order/:id/cancel", internalHandler.CancelForVendorOrder)
	}

	// The carrier calls this directly, with no bearer token — its signature
	// is the only trust boundary, verified inside the handler.
	r.POST("/api/webhooks/shipment-carrier", webhookHandler.Handle)

	return r
}
