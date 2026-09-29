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

	rp := &httputil.ReverseProxy{Rewrite: func(req *httputil.ProxyRequest) {
		req.SetURL(target)
		req.Out.Host = target.Host
		// The Gin handler resolves only explicitly trusted proxy chains. Never
		// forward caller-supplied X-Forwarded-For as the rate-limit identity.
		req.Out.Header.Set("X-Forwarded-For", req.In.Header.Get("X-Real-IP"))
	}}

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
		c.Request.Header.Set("X-Real-IP", c.ClientIP())
		c.Request.Header.Set(middleware.RequestIDHeader, middleware.GetRequestID(c))
		rp.ServeHTTP(c.Writer, c.Request)
	}, nil
}
