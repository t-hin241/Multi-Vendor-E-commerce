// Package transport wires Order's HTTP router: middleware, health checks,
// buyer checkout/orders/returns/support cases, the vendor's own sub-orders,
// returns and support cases, admin operations, and the
// service-authenticated internal contracts.
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
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/pkg/telemetry"
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
	supportHandler *SupportHandler,
	cancellationHandler *CancellationHandler,
	deliveryHandler *DeliveryExceptionHandler,
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
	r.Use(boundRequest())

	health.RegisterRoutes(r, checkers...)

	requireAuth := middleware.RequireAuth(jwtManager)

	buyerGroup := r.Group("/api/orders", requireAuth, middleware.RequireRole("buyer"))
	{
		buyerGroup.POST("/checkout", orderHandler.Checkout)
		buyerGroup.POST("/checkout/preview", orderHandler.Preview)
		buyerGroup.GET("/mine", orderHandler.ListMine)
		buyerGroup.GET("/:id", orderHandler.Get)
		buyerGroup.GET("/:id/policy-snapshot", orderHandler.PolicySnapshot)
		buyerGroup.POST("/:id/cancel", orderHandler.Cancel)
		buyerGroup.POST("/:id/return-requests", returnHandler.Create)
		buyerGroup.GET("/return-requests/mine", returnHandler.ListMine)
		// AF-05: how to send a return back, and the parcel's dispatch.
		buyerGroup.GET("/return-requests/:id/shipping-instructions", returnHandler.ShippingInstructions)
		buyerGroup.POST("/return-requests/:id/dispatches", returnHandler.Dispatch)
		// AF-03: cancel a paid package before handover.
		buyerGroup.POST("/vendor-orders/:id/cancellation-requests", cancellationHandler.Create)
		buyerGroup.GET("/:id/cancellation-requests", cancellationHandler.OrderList)
		buyerGroup.GET("/cancellation-requests/:requestID", cancellationHandler.Get)
		// AF-04: failed delivery; the buyer answers a redelivery offer.
		buyerGroup.GET("/:id/delivery-exceptions", deliveryHandler.OrderList)
		buyerGroup.GET("/delivery-exceptions/:exceptionID", deliveryHandler.Get)
		buyerGroup.POST("/delivery-exceptions/:exceptionID/redelivery-consents", deliveryHandler.Consent)

		buyerGroup.GET("/support-cases/capability", supportHandler.Capability)
		buyerGroup.POST("/:id/support-cases", supportHandler.Create)
		buyerGroup.GET("/support-cases", supportHandler.List)
		buyerGroup.GET("/support-cases/:caseID", supportHandler.Get)
		buyerGroup.GET("/support-cases/:caseID/messages", supportHandler.Messages)
		buyerGroup.POST("/support-cases/:caseID/messages", supportHandler.PostMessage)
		buyerGroup.POST("/support-cases/:caseID/reopen", supportHandler.Reopen)
		buyerGroup.POST("/support-cases/:caseID/confirm", supportHandler.Confirm)
		buyerGroup.GET("/support-cases/:caseID/attachments/:attachmentID", supportHandler.Attachment)
		buyerGroup.POST("/support-attachments", supportHandler.Upload)
		// PW-012: a request without an order id, linked by an admin later.
		buyerGroup.POST("/support-intakes", supportHandler.CreateIntake)
		buyerGroup.GET("/support-intakes", supportHandler.MyIntakes)

		buyerGroup.POST("/addresses", addressHandler.Add)
		buyerGroup.GET("/addresses", addressHandler.ListMine)
		buyerGroup.PATCH("/addresses/:id", addressHandler.Update)
		buyerGroup.DELETE("/addresses/:id", addressHandler.Delete)
		buyerGroup.PATCH("/addresses/:id/default", addressHandler.SetDefault)
	}

	// AF-17: shop members on a buyer or vendor account act as the shop
	// here; each use case checks the shop permission with Vendor.
	vendorGroup := r.Group("/api/orders/vendor", requireAuth, middleware.SellerConsole())
	{
		vendorGroup.GET("/return-requests", returnHandler.VendorList)
		vendorGroup.POST("/:id/cancellation-requests", cancellationHandler.Create)
		vendorGroup.GET("/cancellation-requests", cancellationHandler.VendorList)
		vendorGroup.GET("/cancellation-requests/:requestID", cancellationHandler.Get)
		vendorGroup.GET("/delivery-exceptions", deliveryHandler.VendorList)
		vendorGroup.GET("/delivery-exceptions/:exceptionID", deliveryHandler.Get)
		vendorGroup.POST("/delivery-exceptions/:exceptionID/receipts", deliveryHandler.Receipt)
		vendorGroup.POST("/return-requests/:id/confirm", returnHandler.ConfirmByVendor)
		vendorGroup.POST("/return-requests/:id/receive", returnHandler.Receive)
		vendorGroup.POST("/return-requests/:id/goods-receipts", returnHandler.GoodsReceipt)
		vendorGroup.GET("/support-cases/capability", supportHandler.Capability)
		vendorGroup.GET("/support-cases", supportHandler.List)
		vendorGroup.GET("/support-cases/:caseID", supportHandler.Get)
		vendorGroup.GET("/support-cases/:caseID/messages", supportHandler.Messages)
		vendorGroup.POST("/support-cases/:caseID/messages", supportHandler.PostMessage)
		vendorGroup.GET("/support-cases/:caseID/attachments/:attachmentID", supportHandler.Attachment)
		vendorGroup.POST("/support-attachments", supportHandler.Upload)
		vendorGroup.GET("/mine", orderHandler.ListVendorMine)
		vendorGroup.GET("/:id/policy-snapshot", orderHandler.PolicySnapshot)
		vendorGroup.GET("/summary", orderHandler.Summary)
		vendorGroup.GET("/export.csv", orderHandler.ExportCSV)
		vendorGroup.PATCH("/:vendorOrderID/status", orderHandler.UpdateVendorOrderStatus)
	}

	adminGroup := r.Group("/api/orders/admin", requireAuth, middleware.RequireRole("admin"), adminGuard)
	{
		adminGroup.GET("", adminHandler.List)
		adminGroup.GET("/refunds", adminHandler.ListRefunds)
		adminGroup.GET("/payment-exceptions", adminHandler.PaymentExceptions)
		adminGroup.GET("/operations", adminHandler.Operations)
		adminGroup.POST("/operations/effects/:effectID/replay", adminHandler.ReplayEffect)
		adminGroup.GET("/return-requests", returnHandler.AdminList)
		adminGroup.GET("/return-requests/:id", returnHandler.AdminGet)
		adminGroup.GET("/return-requests/:id/history", returnHandler.History)
		adminGroup.POST("/return-requests/:id/decision", returnHandler.Decide)
		adminGroup.POST("/return-requests/:id/receive", returnHandler.Receive)
		adminGroup.POST("/return-requests/:id/retry-refund", returnHandler.RetryRefund)
		adminGroup.POST("/return-requests/:id/goods-receipts", returnHandler.GoodsReceipt)
		adminGroup.POST("/return-requests/:id/shipping-authorizations", returnHandler.AuthorizeShipping)
		adminGroup.POST("/return-requests/:id/shipping-decisions", returnHandler.ShippingDecision)
		adminGroup.POST("/return-requests/:id/receipt-corrections", returnHandler.ReceiptCorrection)
		adminGroup.GET("/support-cases/capability", supportHandler.Capability)
		adminGroup.GET("/support-cases", supportHandler.List)
		adminGroup.GET("/support-cases/:caseID", supportHandler.Get)
		adminGroup.GET("/support-cases/:caseID/messages", supportHandler.Messages)
		adminGroup.POST("/support-cases/:caseID/messages", supportHandler.PostMessage)
		adminGroup.POST("/support-cases/:caseID/assignments", supportHandler.Assign)
		adminGroup.POST("/support-cases/:caseID/status", supportHandler.ChangeStatus)
		adminGroup.POST("/support-cases/:caseID/resolutions", supportHandler.Resolve)
		adminGroup.POST("/support-cases/:caseID/refunds", supportHandler.Refund)
		adminGroup.POST("/support-cases/:caseID/close", supportHandler.Close)
		adminGroup.GET("/support-cases/:caseID/attachments/:attachmentID", supportHandler.Attachment)
		adminGroup.POST("/support-attachments", supportHandler.Upload)
		adminGroup.GET("/support-intakes", supportHandler.AdminIntakes)
		adminGroup.GET("/cancellation-requests", cancellationHandler.AdminList)
		adminGroup.GET("/cancellation-requests/:requestID", cancellationHandler.Get)
		adminGroup.POST("/cancellation-requests/:requestID/decisions", cancellationHandler.Decide)
		adminGroup.GET("/delivery-exceptions", deliveryHandler.AdminList)
		adminGroup.GET("/delivery-exceptions/:exceptionID", deliveryHandler.Get)
		adminGroup.POST("/delivery-exceptions/:exceptionID/receipts", deliveryHandler.Receipt)
		adminGroup.POST("/delivery-exceptions/:exceptionID/decisions", deliveryHandler.Decide)
		supportHandler.RegisterReceiptEvidence(vendorGroup, adminGroup)
		adminGroup.POST("/support-intakes/:intakeID/links", supportHandler.LinkIntake)
		adminGroup.POST("/support-intakes/:intakeID/closure", supportHandler.CloseIntake)
		adminGroup.GET("/commission-rules", adminHandler.ListCommissionRules)
		adminGroup.POST("/commission-rules", adminHandler.SetCommissionRule)
		adminGroup.GET("/:id", adminHandler.Get)
		adminGroup.GET("/:id/policy-snapshot", orderHandler.PolicySnapshot)
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
		// AF-03 fence: Shipment claims the grant before handover.
		internalGroup.POST("/orders/fulfillment-grants/:id/claims", internal.Allow("shipment"), cancellationHandler.ClaimHandover)
		internalGroup.POST("/inventory-events", internal.Allow("inventory"), internalHandler.InventoryEvent)
		internalGroup.POST("/refund-events", payment, internalHandler.RefundEvent)
		internalGroup.POST("/settlements/holds", payment, internalHandler.SettlementHolds)
		internalGroup.POST("/shipment-events", internal.Allow("shipment"), internalHandler.ShipmentEvent)
		internalGroup.POST("/shipment-exceptions", internal.Allow("shipment"), deliveryHandler.ShipmentException)
		internalGroup.GET("/policy-rules/readiness", internal.Allow("vendor"), internalHandler.PolicyRuleReadiness)
		internalGroup.POST("/policy-published", internal.Allow("vendor"), internalHandler.PolicyPublished)
	}

	return r
}

// boundRequest caps request body size and processing time. Support
// attachment uploads get room for one image; everything else is JSON.
func boundRequest() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		if c.Request.Body != nil {
			limit := int64(1 << 20)
			if isAttachmentUpload(c.FullPath()) {
				limit = maxAttachmentRequestBytes
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}
