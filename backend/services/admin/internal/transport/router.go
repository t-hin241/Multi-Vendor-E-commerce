// Package transport wires the Admin service's HTTP router: middleware,
// health checks, and the read-only operations dashboard and audit search.
package transport

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaudit"
	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/telemetry"
	"shopee/backend/services/admin/internal/usecase"
)

// NewRouter builds the service's Gin engine. Admin exposes only GET routes:
// actions stay in the domain service that owns the data, with its own
// checks and audit.
func NewRouter(env string, log zerolog.Logger, jwt *authjwt.Manager, svc usecase.Service, adminGuard gin.HandlerFunc, checkers ...health.Checker) *gin.Engine {
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(telemetry.Middleware())
	r.Use(middleware.StructuredLogging(log))
	r.Use(middleware.Recovery(log))
	r.Use(func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})

	health.RegisterRoutes(r, checkers...)

	h := handler{svc: svc, log: log}
	admin := r.Group("/api/admin", middleware.RequireAuth(jwt), middleware.RequireRole("admin"), adminGuard, func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Next()
	})
	admin.GET("/dashboard", h.dashboard)
	admin.GET("/audit", h.audit)
	admin.GET("/work-items", func(c *gin.Context) {
		out, err := h.svc.WorkItems(c.Request.Context(), middleware.GetUserID(c), c.GetHeader("Authorization"), c.Request.URL.Query())
		if err != nil {
			httpresponse.HandleError(c, h.log, err)
			return
		}
		httpresponse.OK(c, http.StatusOK, out)
	})
	return r
}

type handler struct {
	svc usecase.Service
	log zerolog.Logger
}

// dashboard: the operations counters of every service, with the freshness
// and availability of each.
func (h handler) dashboard(c *gin.Context) {
	out, err := h.svc.Dashboard(c.Request.Context(), middleware.GetUserID(c), c.GetHeader("Authorization"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, out)
}

// audit: ?source=&actor_id=&entity_type=&entity_id=&action=&request_id=
// &from=&to=&limit=&cursor= (cursor from the previous page).
func (h handler) audit(c *gin.Context) {
	q := c.Request.URL.Query()
	for _, internal := range []string{"cursor_time", "cursor_id"} {
		q.Del(internal)
	}
	f, err := adminaudit.ParseFilter(q)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	page, err := h.svc.SearchAudit(c.Request.Context(), middleware.GetUserID(c), c.GetHeader("Authorization"), f, c.Query("cursor"), c.Query("source"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, page)
}
