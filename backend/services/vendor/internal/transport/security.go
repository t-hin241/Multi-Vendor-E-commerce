package transport

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"shopee/backend/pkg/httpresponse"
)

func requestBounds() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 6<<20)
		for _, key := range []string{"vendorId", "id", "userID"} {
			if value := c.Param(key); value != "" {
				if _, err := uuid.Parse(value); err != nil {
					httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid identifier")
					c.Abort()
					return
				}
			}
		}
		c.Next()
	}
}
