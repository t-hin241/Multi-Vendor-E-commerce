// Package transport wires this service's HTTP router: middleware, health
// checks, and (once implemented) its domain endpoints.
package transport

import (
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/health"
	"shopee/backend/pkg/middleware"
)

// NewRouter builds the service's Gin engine. Domain routes get added here as
// this service's use cases are implemented in later phases.
func NewRouter(env string, log zerolog.Logger, checkers ...health.Checker) *gin.Engine {
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(middleware.StructuredLogging(log))
	r.Use(middleware.Recovery(log))

	health.RegisterRoutes(r, checkers...)

	return r
}
