// Package proxy builds the reverse-proxy handlers the gateway uses to
// forward each public API prefix to the backend service that owns it.
package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
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

	rp := &httputil.ReverseProxy{Transport: telemetry.Transport(upstreamTransport), Rewrite: func(req *httputil.ProxyRequest) {
		req.SetURL(target)
		req.Out.Host = target.Host
		// The Gin handler resolves only explicitly trusted proxy chains. Never
		// forward caller-supplied X-Forwarded-For as the rate-limit identity.
		req.Out.Header.Set("X-Forwarded-For", req.In.Header.Get("X-Real-IP"))
		// Service-to-service credentials never come from the public side.
		for _, h := range internalCredentialHeaders {
			req.Out.Header.Del(h)
		}
	}}

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		requestID := r.Header.Get(middleware.RequestIDHeader)
		log.Error().
			Err(err).
			Str("upstream", upstreamBaseURL).
			Str("path", r.URL.Path).
			Str("request_id", requestID).
			Msg("upstream_proxy_error")
		status, code, message := http.StatusBadGateway, "upstream_unavailable", "Service temporarily unavailable"
		if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
			status, code, message = http.StatusGatewayTimeout, "upstream_timeout", "The service did not answer in time"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": requestID}})
		_, _ = w.Write(body)
	}

	return func(c *gin.Context) {
		c.Request.Header.Set("X-Real-IP", c.ClientIP())
		c.Request.Header.Set(middleware.RequestIDHeader, middleware.GetRequestID(c))
		rp.ServeHTTP(c.Writer, c.Request)
	}, nil
}

// internalCredentialHeaders are how services authenticate each other
// (pkg/serviceauth and the two single-purpose keys); a client sending them
// gets them dropped at the edge.
var internalCredentialHeaders = []string{serviceauth.Header, serviceauth.CallerHeader, "X-Reset-Delivery-Key", "X-Vendor-Payout-Key"}

// upstreamTransport bounds every call to a service (PLT-04): connecting
// takes at most 5s and the first byte of the answer at most 30s, so a
// stuck service shows as a 504 instead of holding the client.
var upstreamTransport = &http.Transport{
	Proxy:                 nil,
	DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	MaxIdleConns:          200,
	MaxIdleConnsPerHost:   50,
	IdleConnTimeout:       90 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
	ExpectContinueTimeout: time.Second,
}
