// Command server runs the inventory service.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"shopee/backend/pkg/adminaudit"
	sessionconfig "shopee/backend/pkg/config"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/identityclient"
	"shopee/backend/pkg/logger"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/shutdown"
	"shopee/backend/pkg/telemetry"
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

	busPassword, err := sessionconfig.RequireEventBusPassword()
	if err != nil {
		log.Fatal().Err(err).Msg("event bus configuration invalid")
	}
	bus, err := eventbus.Start(cfg.Base.NATSURL, eventbus.Credentials{Service: "inventory", Password: busPassword}, cfg.Base.EventPublishing, dbPool, log)
	if err != nil {
		log.Fatal().Err(err).Msg("event bus configuration invalid")
	}

	jwtManager, err := sessionconfig.LoadTokenVerifier()
	if err != nil {
		log.Fatal().Err(err).Msg("access token verifier configuration invalid")
	}
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

	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, itemHandler, internalHandler, adminHandler, internalServices.Verifier,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return dbPool.Ping(ctx) }},
	)

	workerCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	// PLT-03: outbox events go to the event bus (HTTP in rollback mode).
	orderClient := adapter.OrderClient{URL: cfg.OrderServiceURL, Key: internalServices.Key}
	var orderEvents usecase.OrderOperations = orderClient
	invalidateStock := catalogClient.InvalidateStock
	if bus.Publish {
		orderEvents = adapter.BusOrderEvents{OrderClient: orderClient, Bus: bus.Bus}
		invalidateStock = adapter.BusStockInvalidator(bus.Bus)
	}
	maintenance := usecase.Maintenance{InvalidateStock: invalidateStock, Repository: repository.Maintenance{Pool: dbPool}, Reservations: reservationRepo, Orders: orderEvents, Identity: identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}, Log: log, ExpiryEnabled: cfg.ExpiryEnabled}
	transport.RegisterOperations(router, jwtManager, maintenance, log)
	go maintenance.Run(workerCtx)
	adminaudit.Register(router.Group("/api/inventory/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin")), "/audit-events",
		adminaudit.Source{Name: "inventory", SQL: repository.AuditSearchSQL, DB: dbPool, Roles: identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}}, log)

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

	shutdown.WaitForSignal(log, srv, cfg.Base.ShutdownTimeout, func() {
		stopWorker()
		bus.Close(cfg.Base.ShutdownTimeout)
	})
}
