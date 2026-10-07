// Command server runs the review service: verified-purchase reviews,
// shop replies and reports, admin moderation, and the background cleanup
// of failed image uploads.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"shopee/backend/pkg/adminaccess"
	"shopee/backend/pkg/adminaudit"
	sessionconfig "shopee/backend/pkg/config"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/identityclient"
	"shopee/backend/pkg/logger"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/platform/objectstorage"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/platform/redisclient"
	"shopee/backend/pkg/shutdown"
	"shopee/backend/pkg/telemetry"
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

	telemetryCfg, err := sessionconfig.LoadTelemetry()
	if err != nil {
		log.Fatal().Err(err).Msg("telemetry configuration invalid")
	}
	tel, err := telemetry.Setup("review", telemetry.Options(telemetryCfg), log)
	if err != nil {
		log.Fatal().Err(err).Msg("telemetry start failed")
	}
	defer tel.Close()
	ctx := context.Background()
	db, err := postgres.NewPool(ctx, cfg.Base.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("database connection failed")
	}
	defer db.Close()
	if err := telemetry.RegisterDBPool(db); err != nil {
		log.Fatal().Err(err).Msg("database pool metrics failed")
	}
	// Plan 14: the runtime role reads and writes rows of this database only.
	if problems, err := postgres.CheckRuntimeRole(ctx, db, cfg.Base.Env == "production"); err != nil {
		log.Fatal().Err(err).Msg("database role check failed")
	} else if len(problems) > 0 {
		log.Warn().Strs("problems", problems).Msg("database_role_too_powerful")
	}
	redis, err := redisclient.NewClient(ctx, cfg.Base.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("redis connection failed")
	}
	if err := telemetry.RegisterRedisPool(redis); err != nil {
		log.Fatal().Err(err).Msg("redis pool metrics failed")
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
	jwtManager, err := sessionconfig.LoadTokenVerifier()
	if err != nil {
		log.Fatal().Err(err).Msg("access token verifier configuration invalid")
	}
	verifier, err := sessionconfig.LoadSessionVerifier()
	if err != nil {
		log.Fatal().Err(err).Msg("session verifier configuration invalid")
	}
	jwtManager.SetVerifier(verifier)
	limiter := adapter.RedisRateLimiter{Client: redis, Prefix: "review:rate:"}
	// AF-19: every admin route needs the bundle named in transport.AdminRoutes.
	adminGuard := adminaccess.Guard(adminaccess.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}, transport.AdminRoutes, log)
	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, transport.NewHandler(uc, log), limiter, adminGuard,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return db.Ping(ctx) }},
		health.Checker{Name: "redis", Ping: func(ctx context.Context) error { return redis.Ping(ctx).Err() }},
		health.Checker{Name: "object_storage", Ping: store.Ping})
	adminaudit.Register(router.Group("/api/reviews/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin"), adminGuard), "/audit-events",
		adminaudit.Source{Name: "review", SQL: repository.AuditSearchSQL, DB: db, Roles: roles}, log)

	srv := &http.Server{Addr: ":" + cfg.Base.Port, Handler: router, ReadHeaderTimeout: cfg.Base.HTTPReadTimeout, ReadTimeout: cfg.Base.HTTPReadTimeout,
		WriteTimeout: cfg.Base.HTTPWriteTimeout, IdleTimeout: cfg.Base.HTTPIdleTimeout}
	go func() {
		log.Info().Str("port", cfg.Base.Port).Msg("review_starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("review_listen_failed")
		}
	}()
	shutdown.WaitForSignal(log, srv, cfg.Base.ShutdownTimeout, stopMaintenance)
}
