package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
)

// Recovery converts a panic in a handler into a logged error and a uniform
// 500 response, instead of letting it crash the process or leak a stack
// trace to the client.
func Recovery(log zerolog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Str("request_id", GetRequestID(c)).
					Interface("panic", r).
					Str("path", c.Request.URL.Path).
					Msg("panic_recovered")

				httpresponse.Error(c, http.StatusInternalServerError, "internal_error", "Something went wrong")
				c.Abort()
			}
		}()
		c.Next()
	}
}
