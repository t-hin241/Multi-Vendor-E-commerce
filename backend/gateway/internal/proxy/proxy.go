// Package proxy builds the reverse-proxy handlers the gateway uses to
// forward each public API prefix to the backend service that owns it.
package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/middleware"
)

// NewReverseProxyHandler returns a Gin handler that forwards the request to
// upstreamBaseURL unchanged (same path, method, headers and body), adding
// the correlated request id so downstream service logs can be joined back
// to this gateway request.
func NewReverseProxyHandler(upstreamBaseURL string, log zerolog.Logger) (gin.HandlerFunc, error) {
	target, err := url.Parse(upstreamBaseURL)
	if err != nil {
		return nil, err
	}

	rp := httputil.NewSingleHostReverseProxy(target)

	originalDirector := rp.Director
	rp.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
	}

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Error().
			Err(err).
			Str("upstream", upstreamBaseURL).
			Str("path", r.URL.Path).
			Msg("upstream_proxy_error")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"code":"upstream_unavailable","message":"Service temporarily unavailable"}}`))
	}

	return func(c *gin.Context) {
		c.Request.Header.Set(middleware.RequestIDHeader, middleware.GetRequestID(c))
		rp.ServeHTTP(c.Writer, c.Request)
	}, nil
}
