// Package transport wires Payment's HTTP router: middleware, health checks,
// the buyer payment flow, the public webhook, admin reconciliation and
// settlement, and the internal contracts Order and Vendor use.
package transport

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
)

type Handlers struct {
	Payment  *PaymentHandler
	Webhook  *WebhookHandler
	Refund   *RefundHandler
	Admin    *AdminHandler
	Approval *ApprovalHandler
	// AdminGuard enforces AdminRoutes (AF-19).
	AdminGuard gin.HandlerFunc
}

func NewRouter(env string, log zerolog.Logger, jwtManager *authjwt.Manager, h Handlers, internal *serviceauth.Verifier, checkers ...health.Checker) *gin.Engine {
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(telemetry.Middleware())
	r.Use(middleware.StructuredLogging(log))
	r.Use(middleware.Recovery(log))
	r.Use(boundRequest())

	health.RegisterRoutes(r, checkers...)

	buyerGroup := r.Group("/api/payments", middleware.RequireAuth(jwtManager), middleware.RequireRole("buyer"))
	{
		buyerGroup.POST("/intents", h.Payment.CreateIntent)
		buyerGroup.GET("/intents/:id", h.Payment.Get)
		buyerGroup.POST("/intents/:id/simulate", h.Payment.Simulate)
	}

	adminGroup := r.Group("/api/payments/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin"), h.AdminGuard, noStore())
	{
		adminGroup.GET("/refunds", h.Refund.AdminList)
		adminGroup.GET("/refunds/:id", h.Refund.AdminGet)
		adminGroup.POST("/refunds/:id/resolve", h.Refund.AdminResolve)

		adminGroup.GET("/reconciliation", h.Admin.Overview)
		adminGroup.GET("/search", h.Admin.Search)
		adminGroup.GET("/audit", h.Admin.History)
		adminGroup.POST("/receipts/:id/retry", h.Admin.RetryReceipt)
		adminGroup.POST("/intents/:id/reconcile", h.Admin.ReconcileIntent)
		adminGroup.POST("/order-sync/:id/retry", h.Admin.RetryOrderSync)
		adminGroup.POST("/refund-sync/:id/retry", h.Admin.RetryRefundSync)

		adminGroup.GET("/settlements/balances", h.Admin.Balances)
		adminGroup.GET("/settlements/vendors/:vendorId/entries", h.Admin.Statement)
		adminGroup.POST("/settlements/adjustments", h.Admin.Adjust)
		adminGroup.GET("/payouts/batches", h.Admin.ListBatches)
		adminGroup.POST("/payouts/batches", h.Admin.CreateBatch)
		adminGroup.GET("/payouts/batches/:id", h.Admin.GetBatch)
		adminGroup.POST("/payouts/items/:id/resolve", h.Admin.ResolvePayoutItem)

		// AF-19 maker-checker: draft -> submission (maker, password proof)
		// -> decision (another admin with finance.approve, password proof).
		adminGroup.GET("/approval-requests", h.Approval.List)
		adminGroup.GET("/approval-requests/:id", h.Approval.Get)
		adminGroup.POST("/approval-requests", h.Approval.Create)
		adminGroup.POST("/approval-requests/:id/submission", h.Approval.Submit)
		adminGroup.POST("/approval-requests/:id/cancellation", h.Approval.Cancel)
		adminGroup.POST("/approval-requests/:id/decisions", h.Approval.Decide)
	}

	internalGroup := r.Group("/internal", internal.Allow("order"))
	{
		internalGroup.POST("/payments/refunds", h.Refund.Request)
		internalGroup.POST("/settlements/vendor-orders", h.Admin.IngestVendorOrder)
	}

	// The provider calls this directly, with no bearer token; its signature
	// is the only trust boundary, verified inside the handler.
	r.POST("/api/webhooks/payments", h.Webhook.Handle)

	return r
}

// boundRequest caps processing time and body size for every route; the
// webhook applies its own tighter limit.
func boundRequest() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		if c.Request.Body != nil && !strings.HasPrefix(c.Request.URL.Path, "/api/webhooks/") {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		}
		c.Next()
	}
}

func noStore() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}
