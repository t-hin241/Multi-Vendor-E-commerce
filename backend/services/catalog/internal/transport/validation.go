package transport

import (
	"context"
	"net/http"
	"shopee/backend/pkg/httpresponse"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func validateRequest() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		fail := func() {
			httpresponse.Error(c, 400, "validation_error", "Invalid identifier, search or sort parameter")
			c.Abort()
		}
		for _, key := range []string{"vendor_id", "category_id"} {
			if raw := c.Query(key); raw != "" {
				if _, err := uuid.Parse(raw); err != nil {
					fail()
					return
				}
			}
		}
		for _, param := range c.Params {
			if c.FullPath() == "/api/catalog/products/:id" && c.Request.Method == http.MethodGet {
				continue
			}
			if _, err := uuid.Parse(param.Value); err != nil {
				fail()
				return
			}
		}
		if len(c.Query("q")) > 200 {
			fail()
			return
		}
		switch c.Query("sort") {
		case "", "newest", "price_asc", "price_desc":
		default:
			fail()
			return
		}
		if !strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/form-data") {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		}
		c.Next()
	}
}
