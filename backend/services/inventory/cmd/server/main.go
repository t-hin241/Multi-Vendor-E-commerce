// Command server runs the inventory service.
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
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/platform/redisclient"
	"shopee/backend/pkg/shutdown"
	"shopee/backend/services/inventory/internal/adapter"
	"shopee/backend/services/inventory/internal/config"
	"shopee/backend/services/inventory/internal/repository"
	"shopee/backend/services/inventory/internal/transport"
	"shopee/backend/services/inventory/internal/usecase"
)

const serviceName = "inventory"

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, serviceName+": config error:", err)
		os.Exit(1)
	}

	internalServices, err := sessionconfig.LoadInternalServices()
	if err != nil {
		fmt.Fprintln(os.Stderr, "internal service configuration invalid")
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
	vendorClient := adapter.NewHTTPVendorClient(cfg.VendorServiceURL, internalServices.Key)
	catalogClient := adapter.NewHTTPCatalogClient(cfg.CatalogServiceURL, internalServices.Key)

	itemRepo := repository.NewInventoryItemRepository(dbPool)
	reservationRepo := repository.NewReservationRepository(dbPool)
	restockRequestRepo := repository.NewRestockRequestRepository(dbPool)

	inventoryUseCase := usecase.NewInventoryUseCase(itemRepo, reservationRepo, restockRequestRepo, vendorClient, catalogClient, usecase.Operations{Transactions: repository.Transactions{Pool: dbPool}, Identity: identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}, Audit: repository.AdminAudit{Pool: dbPool}})

	itemHandler := transport.NewItemHandler(inventoryUseCase, log)
	internalHandler := transport.NewInternalHandler(inventoryUseCase, log)
	adminHandler := transport.NewAdminHandler(inventoryUseCase, log)

	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, itemHandler, internalHandler, adminHandler, internalServices.Key,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return dbPool.Ping(ctx) }},
		health.Checker{Name: "redis", Ping: func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }},
		health.Checker{Name: "nats", Ping: func(ctx context.Context) error {
			if !natsConn.IsConnected() {
				return fmt.Errorf("nats: not connected")
			}
			return nil
		}},
	)

	workerCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	maintenance := usecase.Maintenance{InvalidateStock: catalogClient.InvalidateStock, Repository: repository.Maintenance{Pool: dbPool}, Reservations: reservationRepo, Orders: adapter.OrderClient{URL: cfg.OrderServiceURL, Key: internalServices.Key}, Identity: identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}, Log: log, ExpiryEnabled: cfg.ExpiryEnabled}
	transport.RegisterOperations(router, jwtManager, maintenance, log)
	go maintenance.Run(workerCtx)
	adminaudit.Register(router.Group("/api/inventory/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin")), "/audit-events",
		adminaudit.Source{Name: "inventory", SQL: repository.AuditSearchSQL, DB: dbPool, Roles: identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}}, log)

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
