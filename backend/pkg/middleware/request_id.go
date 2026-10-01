// Package middleware provides Gin middleware shared by every backend service.
package middleware

import (
	"context"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RequestIDHeader is the header used to propagate a request id end to end
// across the API Gateway and internal services.
const RequestIDHeader = "X-Request-Id"

// ContextKeyRequestID is the Gin context key the request id is stored under.
const ContextKeyRequestID = "request_id"

type requestIDContextKey struct{}

func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDContextKey{}).(string)
	return id
}

// validRequestID bounds what an inbound id may be: it ends up in logs and
// audit rows, so anything longer or with other characters is replaced.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,64}$`)

// ContextWithRequestID returns ctx carrying id as its request id, as the
// RequestID middleware does for an HTTP request.
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDContextKey{}, id)
}

// CorrelationID is the request id to store with an audit row, or nil when
// the work did not start from a request (a background job).
func CorrelationID(ctx context.Context) *string {
	if id := RequestIDFromContext(ctx); id != "" {
		return &id
	}
	return nil
}

// RequestID assigns a request id to every request, reusing an inbound id
// from the gateway (or another upstream caller) when present so a single
// request can be correlated across service logs. A browser may choose the
// id of a sensitive action up front, so the operation can be looked up in
// the audit even when the response is lost.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader(RequestIDHeader)
		if !validRequestID.MatchString(requestID) {
			requestID = uuid.NewString()
		}

		c.Set(ContextKeyRequestID, requestID)
		c.Request = c.Request.WithContext(ContextWithRequestID(c.Request.Context(), requestID))
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
