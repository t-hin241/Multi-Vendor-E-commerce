package serviceauth

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
)

const Header = "X-Identity-Service-Key"

func SetRequestHeaders(req *http.Request, key string) {
	req.Header.Set(Header, key)
	if id := middleware.RequestIDFromContext(req.Context()); id != "" {
		req.Header.Set(middleware.RequestIDHeader, id)
	}
}

func Require(key, header string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(c.GetHeader(header))) != 1 {
			httpresponse.Error(c, http.StatusForbidden, "forbidden", "Service authentication required")
			c.Abort()
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
		c.Next()
	}
}
