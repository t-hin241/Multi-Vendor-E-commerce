// Command server runs the vendor service.
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
	"shopee/backend/pkg/platform/natsclient"
	"shopee/backend/pkg/platform/objectstorage"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/platform/redisclient"
	"shopee/backend/pkg/shutdown"
	"shopee/backend/services/vendorsvc/internal/adapter"
	"shopee/backend/services/vendorsvc/internal/config"
	"shopee/backend/services/vendorsvc/internal/repository"
	"shopee/backend/services/vendorsvc/internal/transport"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

const serviceName = "vendor"

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, serviceName+": config error:", err)
		os.Exit(1)
	}

	log := logger.New(serviceName, cfg.Base.Env, cfg.Base.LogLevel)
	ctx := context.Background()

	dbPool, err := postgres.NewPool(ctx, cfg.Base.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("database connection failed")
	}
	defer dbPool.Close()

	redisClient, err := redisclient.NewClient(ctx, cfg.Base.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("redis connection failed")
	}
	defer redisClient.Close()

	natsConn, err := natsclient.Connect(cfg.Base.NATSURL)
	if err != nil {
		log.Fatal().Err(err).Msg("nats connection failed")
	}
	defer natsConn.Close()

	jwtManager := authjwt.NewManager(cfg.JWTSecret)
	verifier, err := sessionconfig.LoadSessionVerifier()
	if err != nil {
		log.Fatal().Err(err).Msg("session verifier configuration invalid")
	}
	jwtManager.SetVerifier(verifier)

	objectStore, err := objectstorage.NewClient(ctx, cfg.ObjectStorage)
	if err != nil {
		log.Fatal().Err(err).Msg("object storage connection failed")
	}

	vendorRepo := repository.NewVendorRepository(dbPool)
	auditLogRepo := repository.NewAuditLogRepository(dbPool)
	addressRepo := repository.NewVendorAddressRepository(dbPool)
	outbox := repository.Outbox{Pool: dbPool}
	notices := repository.NotificationOutbox{Pool: dbPool}
	ops := usecase.Operations{Tx: repository.Transactions{Pool: dbPool}, Actors: identityclient.Client{URL: cfg.Internal.IdentityURL, Key: cfg.Internal.Key}, Addresses: addressRepo, Events: outbox, Notices: notices}
	workerCtx, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()
	go adapter.DispatchStatus(workerCtx, outbox, []string{cfg.CatalogURL, cfg.OrderURL}, cfg.Internal.Key, log)
	go adapter.DispatchNotices(workerCtx, notices, cfg.NotificationServiceURL, cfg.Internal.Key, log)
	vendorUseCase := usecase.NewVendorUseCase(vendorRepo, auditLogRepo, objectStore, log, ops)
	addressUseCase := usecase.NewVendorAddressUseCase(addressRepo, vendorRepo, ops)

	vendorHandler := transport.NewVendorHandler(vendorUseCase, log)
	addressHandler := transport.NewVendorAddressHandler(addressUseCase, log)
	adminHandler := transport.NewAdminHandler(vendorUseCase, log)
	internalHandler := transport.NewInternalHandler(vendorUseCase, log)

	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, vendorHandler, addressHandler, adminHandler, internalHandler, cfg.Internal.Key,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return dbPool.Ping(ctx) }},
		health.Checker{Name: "redis", Ping: func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }},
		health.Checker{Name: "object_storage", Ping: objectStore.Ping},
		health.Checker{Name: "nats", Ping: func(ctx context.Context) error {
			if !natsConn.IsConnected() {
				return fmt.Errorf("nats: not connected")
			}
			return nil
		}},
	)

	cipher, err := adapter.NewPayoutCipher(cfg.PayoutKey)
	if err != nil {
		log.Fatal().Msg("payout encryption configuration invalid")
	}
	payoutUC := &usecase.PayoutUseCase{Accounts: repository.PayoutRepository{Pool: dbPool}, Vendors: vendorRepo, Audit: auditLogRepo, Ops: ops, Cipher: cipher}
	(transport.PayoutHandler{UseCase: payoutUC, Log: log}).Register(router, middleware.RequireAuth(jwtManager), cfg.PayoutServiceKey, cfg.Internal.Key)

	dashboard := usecase.Dashboard{Vendors: vendorUseCase, Orders: adapter.ReportClient{URL: cfg.OrderURL, Key: cfg.Internal.Key}, Payments: adapter.ReportClient{URL: cfg.PaymentURL, Key: cfg.Internal.Key}}
	router.GET("/api/vendor/:vendorId/dashboard", middleware.RequireAuth(jwtManager), middleware.RequireRole("vendor"), transport.DashboardHandler(dashboard, log))

	adminaudit.Register(router.Group("/api/vendor/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin")), "/audit-events",
		adminaudit.Source{Name: "vendor", SQL: repository.AuditSearchSQL, DB: dbPool, Roles: identityclient.Client{URL: cfg.Internal.IdentityURL, Key: cfg.Internal.Key}}, log)

	srv := &http.Server{
		Addr:              ":" + cfg.Base.Port,
		Handler:           router,
		ReadHeaderTimeout: cfg.Base.HTTPReadTimeout,
		ReadTimeout:       cfg.Base.HTTPReadTimeout,
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
