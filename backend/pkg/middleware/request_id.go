// Package middleware provides Gin middleware shared by every backend service.
package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RequestIDHeader is the header used to propagate a request id end to end
// across the API Gateway and internal services.
const RequestIDHeader = "X-Request-Id"

// ContextKeyRequestID is the Gin context key the request id is stored under.
const ContextKeyRequestID = "request_id"

// RequestID assigns a request id to every request, reusing an inbound id
// from the gateway (or another upstream caller) when present so a single
// request can be correlated across service logs.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader(RequestIDHeader)
		if requestID == "" {
			requestID = uuid.NewString()
		}

		c.Set(ContextKeyRequestID, requestID)
		c.Header(RequestIDHeader, requestID)
		c.Next()
	}
}

// GetRequestID reads the request id set by RequestID out of the Gin context.
func GetRequestID(c *gin.Context) string {
	if v, ok := c.Get(ContextKeyRequestID); ok {
		if id, ok := v.(string); ok {
			return id
		}
	}
	return ""
}
