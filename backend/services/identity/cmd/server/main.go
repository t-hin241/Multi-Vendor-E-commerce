// Command server runs the identity service.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	sessionconfig "shopee/backend/pkg/config"

	"shopee/backend/pkg/adminaudit"
	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/logger"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/platform/redisclient"
	"shopee/backend/pkg/shutdown"
	"shopee/backend/pkg/telemetry"
	"shopee/backend/services/identity/internal/adapter"
	"shopee/backend/services/identity/internal/config"
	"shopee/backend/services/identity/internal/repository"
	"shopee/backend/services/identity/internal/transport"
	"shopee/backend/services/identity/internal/usecase"
)

const serviceName = "identity"

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, serviceName+": config error:", err)
		os.Exit(1)
	}

	log := logger.New(serviceName, cfg.Base.Env, cfg.Base.LogLevel)

	telemetryCfg, err := sessionconfig.LoadTelemetry()
	if err != nil {
		log.Fatal().Err(err).Msg("telemetry configuration invalid")
	}
	tel, err := telemetry.Setup(serviceName, telemetry.Options(telemetryCfg), log)
	if err != nil {
		log.Fatal().Err(err).Msg("telemetry start failed")
	}
	defer tel.Close()
	ctx := context.Background()

	dbPool, err := postgres.NewPool(ctx, cfg.Base.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("database connection failed")
	}
	defer dbPool.Close()
	if err := telemetry.RegisterDBPool(dbPool); err != nil {
		log.Fatal().Err(err).Msg("database pool metrics failed")
	}
	if err := telemetry.RegisterOutboxes(dbPool, repository.Backlogs...); err != nil {
		log.Fatal().Err(err).Msg("outbox metrics failed")
	}
	// Plan 14: the runtime role reads and writes rows of this database only.
	if problems, err := postgres.CheckRuntimeRole(ctx, dbPool, cfg.Base.Env == "production"); err != nil {
		log.Fatal().Err(err).Msg("database role check failed")
	} else if len(problems) > 0 {
		log.Warn().Strs("problems", problems).Msg("database_role_too_powerful")
	}

	redisClient, err := redisclient.NewClient(ctx, cfg.Base.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("redis connection failed")
	}
	if err := telemetry.RegisterRedisPool(redisClient); err != nil {
		log.Fatal().Err(err).Msg("redis pool metrics failed")
	}
	defer redisClient.Close()

	jwtManager := authjwt.NewManager(cfg.JWTSecret)

	userRepo := repository.NewUserRepository(dbPool)
	refreshTokenRepo := repository.NewRefreshTokenRepository(dbPool)
	passwordResetRepo := repository.NewPasswordResetRepository(dbPool)

	tokenCipher, err := usecase.NewTokenCipher(cfg.ResetEncryptionKey)
	if err != nil {
		log.Fatal().Msg("reset encryption configuration invalid")
	}
	transactions := repository.Transactions{Pool: dbPool}
	authUseCase := usecase.NewAuthUseCase(userRepo, refreshTokenRepo, passwordResetRepo, jwtManager, log, transactions, tokenCipher)
	jwtManager.SetVerifier(authUseCase.ValidateSession)
	resetDelivery := &usecase.ResetDeliveryUseCase{Store: passwordResetRepo, Cipher: tokenCipher, Notifier: adapter.ResetNotifier{BaseURL: cfg.NotificationURL, Key: cfg.ResetDeliveryKey}, ResetURL: cfg.ResetURL, Log: log}
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); resetDelivery.Run(workerCtx) }()
	defer func() { stopWorker(); <-workerDone }()
	authHandler := transport.NewAuthHandler(authUseCase, log, cfg.Base.Env == "production" || cfg.Base.Env == "staging")
	adminUseCase := usecase.NewAdminUseCase(userRepo, refreshTokenRepo, transactions)
	adminHandler := transport.NewAdminHandler(adminUseCase, log)
	internalHandler := transport.NewInternalHandler(authUseCase, log)

	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, authHandler, adminHandler, internalHandler, transport.Security{TrustedProxies: cfg.TrustedProxies, Origins: cfg.Origins, ServiceKey: cfg.ServiceKey, Services: cfg.Internal.Verifier, DeliveryKey: cfg.ResetDeliveryKey, RateKey: cfg.JWTSecret, Redis: redisClient}, resetDelivery,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return dbPool.Ping(ctx) }},
		health.Checker{Name: "redis", Ping: func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }},
	)

	adminaudit.Register(router.Group("/api/auth/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin")), "/audit-events",
		adminaudit.Source{Name: "identity", SQL: repository.AuditSearchSQL, DB: dbPool, Roles: adminUseCase}, log)

	srv := &http.Server{
		Addr:              ":" + cfg.Base.Port,
		Handler:           router,
		ReadHeaderTimeout: cfg.Base.HTTPReadTimeout,
		ReadTimeout:       cfg.Base.HTTPReadTimeout,
		WriteTimeout:      cfg.Base.HTTPWriteTimeout,
		IdleTimeout:       cfg.Base.HTTPIdleTimeout,
	}

	go func() {
		log.Info().Str("port", cfg.Base.Port).Msg(serviceName + "_starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg(serviceName + "_listen_failed")
		}
	}()

	shutdown.WaitForSignal(log, srv, cfg.Base.ShutdownTimeout, nil)
}
