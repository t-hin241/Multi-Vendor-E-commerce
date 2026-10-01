package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"shopee/backend/pkg/middleware"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestRequestID_GeneratesOneWhenAbsent(t *testing.T) {
	router := gin.New()
	router.Use(middleware.RequestID())
	router.GET("/", func(c *gin.Context) {
		id := middleware.GetRequestID(c)
		if middleware.RequestIDFromContext(c.Request.Context()) != id {
			t.Error("request id missing from outbound context")
		}
		if id == "" {
			t.Error("expected a generated request id, got empty string")
		}
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Header().Get(middleware.RequestIDHeader) == "" {
		t.Error("expected response to echo back a request id header")
	}
}

func TestRequestID_ReusesInboundHeader(t *testing.T) {
	router := gin.New()
	router.Use(middleware.RequestID())
	router.GET("/", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(middleware.RequestIDHeader, "upstream-request-id")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if got := w.Header().Get(middleware.RequestIDHeader); got != "upstream-request-id" {
		t.Errorf("expected inbound request id to be reused, got %q", got)
	}
}

func TestRequestID_ReplacesUnsafeInboundHeader(t *testing.T) {
	router := gin.New()
	router.Use(middleware.RequestID())
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, bad := range []string{"short", "has space in it", "semi;colon-0000000", "a123456789b123456789c123456789d123456789e123456789f123456789g12345"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(middleware.RequestIDHeader, bad)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if got := w.Header().Get(middleware.RequestIDHeader); got == bad || got == "" {
			t.Errorf("inbound id %q should have been replaced, got %q", bad, got)
		}
	}
}
