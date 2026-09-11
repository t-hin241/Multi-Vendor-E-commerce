// Package health provides the liveness/readiness endpoints every service and
// the gateway expose so orchestration and monitoring can tell a process is
// up and its dependencies are reachable, without leaking any sensitive data.
package health

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Checker reports whether a single dependency (database, cache, broker) is
// currently reachable.
type Checker struct {
	Name string
	Ping func(ctx context.Context) error
}

// RegisterRoutes wires /healthz (liveness: process is running) and /readyz
// (readiness: dependencies are reachable) onto the given router group.
func RegisterRoutes(r gin.IRouter, checkers ...Checker) {
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		results := gin.H{}
		allHealthy := true

		for _, checker := range checkers {
			if err := checker.Ping(ctx); err != nil {
				results[checker.Name] = "unreachable"
				allHealthy = false
				continue
			}
			results[checker.Name] = "ok"
		}

		status := http.StatusOK
		overall := "ok"
		if !allHealthy {
			status = http.StatusServiceUnavailable
			overall = "degraded"
		}

		c.JSON(status, gin.H{"status": overall, "dependencies": results})
	})
}
