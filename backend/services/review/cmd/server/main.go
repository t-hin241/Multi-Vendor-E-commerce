// Command server runs the review service: verified-purchase reviews,
// shop replies and reports, admin moderation, and the background cleanup
// of failed image uploads.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"shopee/backend/pkg/adminaudit"
	"shopee/backend/pkg/authjwt"
	sessionconfig "shopee/backend/pkg/config"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/identityclient"
	"shopee/backend/pkg/logger"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/platform/objectstorage"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/platform/redisclient"
	"shopee/backend/pkg/shutdown"
	"shopee/backend/services/review/internal/adapter"
	"shopee/backend/services/review/internal/config"
	"shopee/backend/services/review/internal/repository"
	"shopee/backend/services/review/internal/transport"
	"shopee/backend/services/review/internal/usecase"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "review: config error:", err)
		os.Exit(1)
	}
	internalServices, err := sessionconfig.LoadInternalServices()
	if err != nil {
		fmt.Fprintln(os.Stderr, "internal service configuration invalid")
		os.Exit(1)
	}

	log := logger.New("review", cfg.Base.Env, cfg.Base.LogLevel)
	ctx := context.Background()
	db, err := postgres.NewPool(ctx, cfg.Base.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("database connection failed")
	}
	defer db.Close()
	redis, err := redisclient.NewClient(ctx, cfg.Base.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("redis connection failed")
	}
	defer redis.Close()
	store, err := objectstorage.NewClient(ctx, cfg.ObjectStorage)
	if err != nil {
		log.Fatal().Err(err).Msg("object storage connection failed")
	}
	roles := identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}
	uc := usecase.NewReviewUseCase(usecase.Deps{
		Repo: repository.NewReviewRepository(db), Tx: repository.Transactions{Pool: db},
		Orders: adapter.NewHTTPOrderClient(cfg.OrderServiceURL, internalServices.Key), Vendors: adapter.NewHTTPVendorClient(cfg.VendorServiceURL, internalServices.Key),
		Identity: adapter.NewHTTPIdentityClient(cfg.IdentityServiceURL, internalServices.Key), Store: store, Roles: roles, Log: log,
		ShowUnverified: cfg.ShowUnverified,
	})
	if cfg.ShowUnverified {
		log.Warn().Msg("review_unverified_reviews_shown")
	}
	maintenanceCtx, stopMaintenance := context.WithCancel(ctx)
	defer stopMaintenance()
	go uc.Maintenance(maintenanceCtx)
	jwtManager := authjwt.NewManager(cfg.JWTSecret)
	verifier, err := sessionconfig.LoadSessionVerifier()
	if err != nil {
		log.Fatal().Err(err).Msg("session verifier configuration invalid")
	}
	jwtManager.SetVerifier(verifier)
	limiter := adapter.RedisRateLimiter{Client: redis, Prefix: "review:rate:"}
	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, transport.NewHandler(uc, log), limiter,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return db.Ping(ctx) }},
		health.Checker{Name: "redis", Ping: func(ctx context.Context) error { return redis.Ping(ctx).Err() }},
		health.Checker{Name: "object_storage", Ping: store.Ping})
	adminaudit.Register(router.Group("/api/reviews/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin")), "/audit-events",
		adminaudit.Source{Name: "review", SQL: repository.AuditSearchSQL, DB: db, Roles: roles}, log)

	srv := &http.Server{Addr: ":" + cfg.Base.Port, Handler: router, ReadHeaderTimeout: cfg.Base.HTTPReadTimeout, ReadTimeout: cfg.Base.HTTPReadTimeout, IdleTimeout: cfg.Base.HTTPIdleTimeout}
	go func() {
		log.Info().Str("port", cfg.Base.Port).Msg("review_starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("review_listen_failed")
		}
	}()
	shutdown.WaitForSignal(log, srv, cfg.Base.ShutdownTimeout, stopMaintenance)
}
