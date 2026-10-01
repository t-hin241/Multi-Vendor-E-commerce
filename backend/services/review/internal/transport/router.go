package transport

import (
	"context"
	"net/http"
	"time"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

// Limiter counts actions per key (adapter.RedisRateLimiter).
type Limiter interface {
	Allow(ctx context.Context, key string, limit int64, window time.Duration) (bool, error)
}

// rateLimit bounds how often one signed-in user may do action. If the
// counter store is down the request goes through (logged): availability
// of reviews matters more than the limit.
func rateLimit(l Limiter, log zerolog.Logger, action string, limit int64, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if l == nil {
			c.Next()
			return
		}
		ok, err := l.Allow(c.Request.Context(), action+":"+middleware.GetUserID(c), limit, window)
		if err != nil {
			log.Warn().Err(err).Str("action", action).Msg("review_rate_limit_unavailable")
		}
		if !ok {
			httpresponse.Error(c, http.StatusTooManyRequests, "rate_limited", "Too many requests; please try again later")
			c.Abort()
			return
		}
		c.Next()
	}
}

func NewRouter(env string, log zerolog.Logger, jwt *authjwt.Manager, h *Handler, limiter Limiter, checkers ...health.Checker) *gin.Engine {
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(middleware.RequestID(), middleware.StructuredLogging(log), middleware.Recovery(log))
	health.RegisterRoutes(r, checkers...)
	r.GET("/api/reviews/products/:productID", h.ListPublic)
	r.GET("/api/reviews/products/:productID/summary", h.Summary)
	auth := middleware.RequireAuth(jwt)
	buyer := r.Group("/api/reviews", auth, middleware.RequireRole("buyer"))
	buyer.GET("/eligibility", h.Eligibility)
	buyer.POST("", rateLimit(limiter, log, "create", 20, time.Hour), h.Create)
	buyer.POST("/:id/images", rateLimit(limiter, log, "image", 40, time.Hour), h.UploadImage)
	buyer.GET("/mine", h.ListMine)
	vendor := r.Group("/api/reviews/vendor", auth, middleware.RequireRole("vendor"))
	vendor.GET("", h.ListVendor)
	vendor.GET("/summary", h.VendorSummary)
	vendor.PUT("/:id/reply", rateLimit(limiter, log, "reply", 60, time.Hour), h.Reply)
	vendor.POST("/:id/reports", rateLimit(limiter, log, "report", 30, time.Hour), h.Report)
	vendor.GET("/moderation-reasons", h.ActiveReasons)
	admin := r.Group("/api/reviews/admin", auth, middleware.RequireRole("admin"))
	admin.GET("", h.ListAdmin)
	admin.GET("/operations", h.Operations)
	admin.POST("/:id/hide", h.Hide)
	admin.POST("/:id/restore", h.Restore)
	admin.GET("/reports", h.ListReports)
	admin.POST("/reports/:id/resolve", h.ResolveReport)
	admin.GET("/moderation-reasons", h.ListReasons)
	admin.POST("/moderation-reasons", h.CreateReason)
	admin.PATCH("/moderation-reasons/:id", h.UpdateReason)
	return r
}
