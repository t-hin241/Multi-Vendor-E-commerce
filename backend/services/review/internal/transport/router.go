package transport

import (
	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/middleware"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

func NewRouter(env string, log zerolog.Logger, jwt *authjwt.Manager, h *Handler, checkers ...health.Checker) *gin.Engine {
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
	buyer.POST("", h.Create)
	buyer.POST("/:id/images", h.UploadImage)
	buyer.GET("/mine", h.ListMine)
	vendor := r.Group("/api/reviews/vendor", auth, middleware.RequireRole("vendor"))
	vendor.GET("", h.ListVendor)
	vendor.GET("/summary", h.VendorSummary)
	vendor.PUT("/:id/reply", h.Reply)
	vendor.POST("/:id/reports", h.Report)
	vendor.GET("/moderation-reasons", h.ActiveReasons)
	admin := r.Group("/api/reviews/admin", auth, middleware.RequireRole("admin"))
	admin.GET("", h.ListAdmin)
	admin.GET("/reports", h.ListReports)
	admin.POST("/reports/:id/resolve", h.ResolveReport)
	admin.GET("/moderation-reasons", h.ListReasons)
	admin.POST("/moderation-reasons", h.CreateReason)
	admin.PATCH("/moderation-reasons/:id", h.UpdateReason)
	return r
}
