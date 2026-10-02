package transport

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/serviceauth"
)

type Security struct {
	TrustedProxies                   []string
	Origins                          []string
	ServiceKey, DeliveryKey, RateKey string
	// Services verifies the calling service (PLT-01); without it only the
	// shared ServiceKey is accepted.
	Services *serviceauth.Verifier
	Redis    redis.UniversalClient
}

// sessionCallers are the services that verify sessions and re-verify
// admins with Identity (every backend service but the gateway).
var sessionCallers = []string{"vendor", "catalog", "inventory", "cart", "order", "payment", "shipment", "admin", "notification", "review"}

func (s Security) services() *serviceauth.Verifier {
	if s.Services != nil {
		return s.Services
	}
	return serviceauth.SharedKey(s.ServiceKey)
}

func (s Security) BrowserProtection() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Pragma", "no-cache")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
		if c.Request.Method != "GET" && c.Request.Method != "HEAD" && c.Request.Method != "OPTIONS" {
			if c.GetHeader("X-CSRF-Protection") != "1" || !slices.Contains(s.Origins, c.GetHeader("Origin")) {
				httpresponse.Error(c, 403, "forbidden", "Invalid request origin")
				c.Abort()
				return
			}
		}
		c.Next()
	}
}
func serviceKey(key, header string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if key == "" || subtle.ConstantTimeCompare([]byte(c.GetHeader(header)), []byte(key)) != 1 {
			httpresponse.Error(c, 403, "forbidden", "Invalid service identity")
			c.Abort()
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
		c.Next()
	}
}
func (h *AuthHandler) VerifySession(c *gin.Context) {
	var req struct {
		UserID    string `json:"user_id"`
		Role      string `json:"role"`
		SessionID string `json:"session_id"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.AbortWithStatus(400)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	err := h.auth.ValidateSession(ctx, &authjwt.Claims{UserID: req.UserID, Role: req.Role, SessionID: req.SessionID})
	if err == authjwt.ErrInvalidToken {
		c.AbortWithStatus(401)
		return
	}
	if err != nil {
		c.AbortWithStatus(503)
		return
	}
	c.Status(204)
}

const rateScript = `local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('EXPIRE',KEYS[1],ARGV[1]) end; return n`

func (s Security) RateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != "POST" {
			c.Next()
			return
		}
		path := c.FullPath()
		ip := c.ClientIP()
		keys := []string{"ip:" + ip + ":" + path}
		limits := []int64{30}
		if strings.HasSuffix(path, "/refresh") {
			limits[0] = 120
		}
		if strings.HasSuffix(path, "/login") || strings.HasSuffix(path, "/register") || strings.HasSuffix(path, "/password-reset/request") {
			data, err := io.ReadAll(c.Request.Body)
			if err != nil {
				httpresponse.Error(c, 400, "validation_error", "Invalid request body")
				c.Abort()
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(data))
			var input struct {
				Email string `json:"email"`
			}
			if json.Unmarshal(data, &input) == nil {
				// Limit each IP/account pair independently.
				keys = append(keys, "account:"+ip+":"+path+":"+strings.ToLower(strings.TrimSpace(input.Email)))
				limits = append(limits, 5)
			}
		}
		if s.Redis == nil {
			httpresponse.Error(c, 503, "service_unavailable", "Authentication temporarily unavailable")
			c.Abort()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
		defer cancel()
		for i, key := range keys {
			mac := hmac.New(sha256.New, []byte(s.RateKey))
			_, _ = mac.Write([]byte(key))
			fingerprint := hex.EncodeToString(mac.Sum(nil))
			n, err := s.Redis.Eval(ctx, rateScript, []string{"identity:rate:" + fingerprint}, 60).Int64()
			if err != nil {
				httpresponse.Error(c, 503, "service_unavailable", "Authentication temporarily unavailable")
				c.Abort()
				return
			}
			if n > limits[i] {
				c.Header("Retry-After", "60")
				httpresponse.Error(c, 429, "rate_limited", "Too many attempts. Please try again later.")
				c.Abort()
				return
			}
		}
		c.Next()
	}
}
