package repository_test

import (
	"context"
	"flag"
	"net"
	"net/http"
	"testing"
	"time"

	"shopee/backend/pkg/middleware"
	"shopee/backend/services/identity/internal/config"
	"shopee/backend/services/identity/internal/repository"
	"shopee/backend/services/identity/internal/transport"
	"shopee/backend/services/identity/internal/usecase"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

var accountBrowserServer = flag.Bool("account-browser-server", false, "serve the isolated AF-12 browser fixture")

// Explicitly invoked by playwright.account.config.ts. No test-only routes
// are linked into the Identity server binary. setup enforces identity_test.
func TestAccountBrowserServer(t *testing.T) {
	if !*accountBrowserServer {
		t.Skip("run using the AF-12 Playwright configuration")
	}
	f := setup(t)
	if err := usecase.BootstrapAdmin(t.Context(), f.tx, f.users, "af12-admin@example.invalid", "synthetic-browser-password-123", "AF12 admin", "synthetic-browser-operator"); err != nil {
		t.Fatal(err)
	}
	opts, err := redis.ParseURL(config.TestRedisURL())
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	defer client.Close()
	security := transport.Security{Origins: []string{"http://localhost:3100"}, Redis: client, RateKey: "synthetic-browser-rate-key-not-a-secret"}
	delivery := &usecase.ResetDeliveryUseCase{Store: f.resets, Cipher: f.cipher, ResetURL: "http://localhost:3100/reset-password", VerifyURL: "http://localhost:3100/verify-email", ChangeURL: "http://localhost:3100/change-email"}
	verification := &usecase.EmailVerificationUseCase{Store: repository.EmailVerificationRepository{Pool: f.pool}, Users: f.users, Tx: f.tx, Cipher: f.cipher, Enabled: true, TTL: 30 * time.Minute, Log: zerolog.Nop()}
	f.auth.WithEmailVerification(verification)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		if c.GetHeader("Origin") == "http://localhost:3100" {
			c.Header("Access-Control-Allow-Origin", "http://localhost:3100")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type,X-CSRF-Protection")
			c.Header("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
		}
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})
	h := transport.NewAuthHandler(f.auth, zerolog.Nop(), false)
	g := r.Group("/api/auth", security.BrowserProtection(), security.RateLimit())
	g.POST("/register", h.Register)
	g.POST("/login", h.Login)
	g.POST("/refresh", h.Refresh)
	g.POST("/logout", h.Logout)
	g.GET("/me", middleware.RequireAuth(f.jwt), h.Me)
	transport.RegisterAccount(r, f.jwt, security, transport.AccountHandler{UseCase: f.account(), Log: zerolog.Nop()})
	transport.RegisterEmailVerification(r, f.jwt, security, transport.EmailVerificationHandler{UseCase: verification, Log: zerolog.Nop()})
	// A local test mailbox replaces SMTP only. Tokens still travel through
	// the real transaction, encrypted queue, lease and delivery use case.
	r.POST("/test/mailbox", security.BrowserProtection(), middleware.RequireAuth(f.jwt), func(c *gin.Context) {
		kind := c.Query("kind")
		if kind != "email_change" && kind != "email_verification" {
			c.AbortWithStatus(400)
			return
		}
		var id string
		err := f.pool.QueryRow(c.Request.Context(), `UPDATE password_reset_deliveries SET status='sending',lease_until=now()+interval '45 seconds' WHERE id=(SELECT id FROM password_reset_deliveries WHERE user_id=$1 AND kind=$2 AND status='pending' ORDER BY created_at DESC LIMIT 1) RETURNING id`, middleware.GetUserID(c), kind).Scan(&id)
		if err != nil {
			c.AbortWithStatus(404)
			return
		}
		msg, err := delivery.Message(c.Request.Context(), id)
		if err != nil {
			c.AbortWithStatus(500)
			return
		}
		c.JSON(200, msg)
	})
	r.GET("/healthz", func(c *gin.Context) { c.Status(200) })
	stop := make(chan struct{}, 1)
	r.POST("/test/stop", security.BrowserProtection(), func(c *gin.Context) {
		c.Status(204)
		c.Writer.Flush()
		select {
		case stop <- struct{}{}:
		default:
		}
	})
	listener, err := net.Listen("tcp", "127.0.0.1:3999")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: r, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	// Global teardown allows setup to drop the synthetic schema. The timer
	// also bounds the fixture lifetime if the browser runner is interrupted.
	select {
	case err := <-done:
		if err != http.ErrServerClosed {
			t.Fatal(err)
		}
	case <-stop:
	case <-time.After(8 * time.Minute):
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Error(err)
	}
}
