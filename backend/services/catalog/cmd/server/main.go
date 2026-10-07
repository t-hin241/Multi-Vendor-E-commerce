// Command server runs the catalog service.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"shopee/backend/pkg/adminaccess"
	"shopee/backend/pkg/adminaudit"
	sessionconfig "shopee/backend/pkg/config"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/identityclient"
	"shopee/backend/pkg/logger"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/platform/objectstorage"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/shutdown"
	"shopee/backend/pkg/telemetry"
	"shopee/backend/pkg/vendorsales"
	"shopee/backend/services/catalog/internal/adapter"
	"shopee/backend/services/catalog/internal/config"
	"shopee/backend/services/catalog/internal/repository"
	"shopee/backend/services/catalog/internal/transport"
	"shopee/backend/services/catalog/internal/usecase"
)

const serviceName = "catalog"

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
	bus, err := eventbus.Start(cfg.Base.NATSURL, eventbus.Credentials{Service: serviceName, Password: busPassword}, cfg.Base.EventPublishing, dbPool, log)
	if err != nil {
		log.Fatal().Err(err).Msg("event bus configuration invalid")
	}

	objectStore, err := objectstorage.NewClient(ctx, cfg.ObjectStorage)
	if err != nil {
		log.Fatal().Err(err).Msg("object storage connection failed")
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
	orderClient := adapter.NewHTTPOrderClient(cfg.OrderServiceURL, internalServices.Key)
	inventoryClient := adapter.NewHTTPInventoryClient(cfg.InventoryServiceURL, internalServices.Key)

	categoryRepo := repository.NewCategoryRepository(dbPool)
	productRepo := repository.NewProductRepository(dbPool)
	imageRepo := repository.NewProductImageRepository(dbPool)
	mediaRepo := repository.NewProductMediaRepository(dbPool)
	auditLogRepo := repository.NewAuditLogRepository(dbPool)
	storefrontCacheRepo := repository.NewStorefrontCacheRepository(dbPool)
	attributeRepo := repository.NewAttributeRepository(dbPool)
	categoryAttributeRuleRepo := repository.NewCategoryAttributeRuleRepository(dbPool)
	productAttributeValueRepo := repository.NewProductAttributeValueRepository(dbPool)
	productVariantRepo := repository.NewProductVariantRepository(dbPool)
	productPackagingRepo := repository.NewProductPackagingRepository(dbPool)

	categoryUseCase := usecase.NewCategoryUseCase(categoryRepo, identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key})
	attributeUseCase := usecase.NewAttributeUseCase(attributeRepo, categoryAttributeRuleRepo, categoryRepo, identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key})
	productUseCase := usecase.NewProductUseCase(
		productRepo, imageRepo, mediaRepo, categoryRepo, auditLogRepo, vendorClient, objectStore,
		vendorClient, orderClient, storefrontCacheRepo, attributeUseCase, productAttributeValueRepo, productVariantRepo,
		inventoryClient, productPackagingRepo,
		usecase.Operations{Identity: identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}, Transactions: repository.Transactions{Pool: dbPool}, Cleanup: repository.ObjectCleanup{Pool: dbPool}, Log: log},
	)

	categoryHandler := transport.NewCategoryHandler(categoryUseCase, log)
	productHandler := transport.NewProductHandler(productUseCase, log)
	storefrontHandler := transport.NewStorefrontHandler(productUseCase, log)
	adminHandler := transport.NewAdminHandler(productUseCase, log)
	internalHandler := transport.NewInternalHandler(productUseCase, log)
	attributeHandler := transport.NewAttributeHandler(attributeUseCase, log)

	// AF-19: every admin route needs the bundle named in transport.AdminRoutes.
	adminGuard := adminaccess.Guard(adminaccess.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}, transport.AdminRoutes, log)
	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, categoryHandler, productHandler, storefrontHandler, adminHandler, internalHandler, attributeHandler, adminGuard, internalServices.Verifier,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return dbPool.Ping(ctx) }},
		health.Checker{Name: "object_storage", Ping: objectStore.Ping},
	)

	maintenance := transport.MaintenanceHandler{Service: usecase.Maintenance{Repository: repository.Maintenance{Pool: dbPool}, Identity: identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}}, Log: log}
	router.GET("/api/catalog/operations", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin"), adminGuard, maintenance.Stats)
	router.POST("/api/catalog/operations/replay", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin"), adminGuard, maintenance.Replay)
	router.POST("/internal/stock-cache/invalidate", internalServices.Verifier.Allow("inventory"), maintenance.InvalidateStock)
	salesStore := vendorsales.Store{Pool: dbPool}
	router.POST("/internal/vendor-status", internalServices.Verifier.Allow("vendor"), salesStore.Handler(log))
	reconcileCtx, stopReconcile := context.WithCancel(ctx)
	defer stopReconcile()
	go productUseCase.ReconcileCache(reconcileCtx, storefrontCacheRepo)
	go (repository.ObjectCleanup{Pool: dbPool}).Run(reconcileCtx, objectStore, log)
	publishStatus := adapter.BusProductStatusPublisher(bus.Bus)
	if !bus.Publish {
		publishStatus = (adapter.ProductStatusPublisher{URL: cfg.OrderServiceURL, Key: internalServices.Key}).Publish
	}
	go (repository.StatusOutbox{Pool: dbPool, Publish: publishStatus}).Run(reconcileCtx, log)
	// PLT-03: shop status and stock changes arrive from the event bus; the
	// internal HTTP routes stay for producers in rollback mode.
	bus.Run(reconcileCtx,
		eventbus.Subscription{Durable: "catalog-vendor-status", Types: []string{events.VendorStatusChanged}, Handle: salesStore.EventHandler()},
		eventbus.Subscription{Durable: "catalog-stock-cache", Types: []string{events.StockChanged}, Handle: adapter.StockChangedHandler()},
	)
	go (vendorsales.Client{URL: cfg.VendorServiceURL, Key: internalServices.Key}).Reconcile(reconcileCtx, salesStore, log)

	catalogAdmin := router.Group("/api/catalog/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin"), adminGuard)
	bus.RegisterAdmin(catalogAdmin, identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key})
	adminaudit.Register(catalogAdmin, "/audit-events",
		adminaudit.Source{Name: "catalog", SQL: repository.AuditSearchSQL + " UNION ALL " + eventbus.InboxAuditSearchSQL, DB: dbPool, Roles: identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}}, log)

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
		stopReconcile()
		bus.Close(cfg.Base.ShutdownTimeout)
	})
}
