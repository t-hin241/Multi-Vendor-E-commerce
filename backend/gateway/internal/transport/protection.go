package transport

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"shopee/backend/pkg/httpresponse"
)

// Gateway protection (PLT-01/02): what the public entry point refuses
// before any service sees the request.

const (
	// maxUploadBody bounds a multipart upload: the largest legitimate one
	// is a 20MB product video (Catalog accepts up to 21MB with framing).
	maxUploadBody = 22 << 20
	// maxBody bounds any other body: every service caps JSON at 1MB or
	// less, so this only stops floods before they reach a service.
	maxBody = 2 << 20
	// MaxHeaderBytes bounds request headers (http.Server).
	MaxHeaderBytes = 32 << 10
)

// denyInternal refuses paths that are not public API: internal routes and
// anything trying to climb out of a prefix ("..", encoded or not).
func denyInternal() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.Request.URL.EscapedPath()
		decoded, err := url.PathUnescape(raw)
		lower := strings.ToLower(decoded)
		if err != nil || strings.Contains(lower, "/internal") || strings.Contains(lower, "..") || strings.Contains(lower, "\\") ||
			strings.Contains(strings.ToLower(raw), "%2f") {
			httpresponse.Error(c, http.StatusNotFound, "not_found", "No route matches this path")
			c.Abort()
			return
		}
		c.Next()
	}
}

// limitBody caps the request body by kind; each service caps its own
// routes further.
func limitBody() gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := int64(maxBody)
		if strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "multipart/") {
			limit = maxUploadBody
		}
		if c.Request.ContentLength > limit {
			httpresponse.Error(c, http.StatusRequestEntityTooLarge, "payload_too_large", "Request body is too large")
			c.Abort()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

// rateRule is a per-client-IP budget for one kind of request.
type rateRule struct {
	name  string
	limit int // per minute
	match func(method, path, contentType string) bool
}

// rateRules: the first match applies. Identity, Payment and Review keep
// their own finer limits; these stop floods at the edge.
var rateRules = []rateRule{
	{"auth", 30, func(m, p, _ string) bool { return m == http.MethodPost && strings.HasPrefix(p, "/api/auth/") }},
	{"checkout", 20, func(m, p, _ string) bool { return m == http.MethodPost && strings.HasPrefix(p, "/api/orders/checkout") }},
	{"webhook", 600, func(_, p, _ string) bool { return strings.HasPrefix(p, "/api/webhooks") }},
	// Support cases and their messages (AF-01): people type them, so a
	// flood is abuse; attachments fall under the upload rule below.
	{"support", 60, func(m, p, ct string) bool {
		return m == http.MethodPost && strings.HasPrefix(p, "/api/orders/") && strings.Contains(p, "/support-cases") &&
			!strings.HasPrefix(ct, "multipart/")
	}},
	{"upload", 60, func(m, _, ct string) bool { return m == http.MethodPost && strings.HasPrefix(ct, "multipart/") }},
	{"default", 1200, func(string, string, string) bool { return true }},
}

type window struct {
	start time.Time
	count int
}

// rateLimiter counts requests per rule and client IP in one-minute
// windows. It is per gateway instance (one on the VPS); counters reset on
// restart, which only loosens the limit briefly.
type rateLimiter struct {
	mu      sync.Mutex
	windows map[string]*window
	now     func() time.Time
	sweep   time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{windows: map[string]*window{}, now: time.Now}
}

func (l *rateLimiter) allow(key string, limit int) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.sweep) > time.Minute {
		for k, w := range l.windows {
			if now.Sub(w.start) >= time.Minute {
				delete(l.windows, k)
			}
		}
		l.sweep = now
	}
	w, ok := l.windows[key]
	if !ok || now.Sub(w.start) >= time.Minute {
		w = &window{start: now}
		l.windows[key] = w
	}
	w.count++
	return w.count <= limit, time.Minute - now.Sub(w.start)
}

func (l *rateLimiter) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		path, ct := c.Request.URL.Path, c.GetHeader("Content-Type")
		for _, r := range rateRules {
			if !r.match(c.Request.Method, path, ct) {
				continue
			}
			ok, retry := l.allow(r.name+"|"+c.ClientIP(), r.limit)
			if !ok {
				c.Header("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
				httpresponse.Error(c, http.StatusTooManyRequests, "rate_limited", "Too many requests; please try again shortly")
				c.Abort()
				return
			}
			break
		}
		c.Next()
	}
}
