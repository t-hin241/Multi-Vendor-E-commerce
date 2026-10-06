package middleware_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/pkg/middleware"

	"github.com/gin-gonic/gin"
)

func TestRequireAuthRejectsRevokedAndUnavailableSessions(t *testing.T) {
	for _, tt := range []struct {
		name   string
		err    error
		status int
	}{{"active", nil, 200}, {"revoked", authjwt.ErrInvalidToken, 401}, {"unavailable", authjwt.ErrVerificationUnavailable, 503}} {
		t.Run(tt.name, func(t *testing.T) {
			manager := authjwttest.Manager()
			manager.SetVerifier(func(context.Context, *authjwt.Claims) error { return tt.err })
			token, _, err := manager.IssueSessionToken("user", "buyer", "session", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			r := gin.New()
			r.GET("/checkout", middleware.RequireAuth(manager), func(c *gin.Context) { c.Status(200) })
			req := httptest.NewRequest("GET", "/checkout", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("got %d want %d", rec.Code, tt.status)
			}
		})
	}
}
