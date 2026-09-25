package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

// StructuredLogging logs one structured line per request: method, path,
// status, latency and the correlated request id. It never logs request or
// response bodies, so secrets in payloads (passwords, tokens) are never
// captured here.
func StructuredLogging(log zerolog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		log.Info().
			Str("request_id", GetRequestID(c)).
			Str("method", c.Request.Method).
			Str("path", path).
			Int("status", c.Writer.Status()).
			Dur("latency", time.Since(start)).
			Str("client_ip", c.ClientIP()).
			Msg("http_request")
	}
}
