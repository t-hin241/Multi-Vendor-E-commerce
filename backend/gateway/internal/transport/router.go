// Package transport wires the gateway's HTTP router: middleware, CORS,
// health checks and the reverse-proxy routes to each backend service.
package transport

import (
	"sort"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/gateway/internal/config"
	"shopee/backend/gateway/internal/proxy"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
)

// NewRouter builds the gateway's Gin engine.
func NewRouter(cfg config.Config, log zerolog.Logger) (*gin.Engine, error) {
	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.StructuredLogging(log))
	r.Use(middleware.Recovery(log))
	r.Use(cors.New(cors.Config{
		AllowOrigins:     cfg.AllowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", middleware.RequestIDHeader},
		ExposeHeaders:    []string{middleware.RequestIDHeader},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// The gateway has no database/cache of its own in phase 0, so liveness
	// is its readiness too: it is ready as soon as it can accept traffic.
	health.RegisterRoutes(r)

	// Prefixes are dispatched manually via NoRoute, by longest-prefix
	// match, rather than as per-prefix Gin routes. A route registered only
	// as "prefix/*wildcard" doesn't match the bare prefix itself (e.g. GET
	// /api/cart with nothing after it) — Gin's trailing-slash redirect
	// kicks in and a reverse-proxied client bounces on it forever; trying
	// to also register the exact prefix path as a sibling route hits a
	// separate Gin radix-tree edge case. Matching by hand sidesteps both.
	type route struct {
		prefix  string
		handler gin.HandlerFunc
	}
	routes := make([]route, 0, len(cfg.Upstreams))
	for prefix, upstreamURL := range cfg.Upstreams {
		handler, err := proxy.NewReverseProxyHandler(upstreamURL, log)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route{prefix: prefix, handler: handler})
	}
	sort.Slice(routes, func(i, j int) bool { return len(routes[i].prefix) > len(routes[j].prefix) })

	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		for _, rt := range routes {
			if path == rt.prefix || strings.HasPrefix(path, rt.prefix+"/") {
				rt.handler(c)
				return
			}
		}
		httpresponse.Error(c, 404, "not_found", "No route matches this path")
	})

	return r, nil
}
