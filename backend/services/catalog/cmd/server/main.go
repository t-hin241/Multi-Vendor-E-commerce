// Command server runs the catalog service.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

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
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/shutdown"
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

	objectStore, err := objectstorage.NewClient(ctx, cfg.ObjectStorage)
	if err != nil {
		log.Fatal().Err(err).Msg("object storage connection failed")
	}

	jwtManager := authjwt.NewManager(cfg.JWTSecret)
	verifier, err := sessionconfig.LoadSessionVerifier()
	if err != nil {
		log.Fatal().Err(err).Msg("session verifier configuration invalid")
	}
	jwtManager.SetVerifier(verifier)
	vendorClient := adapter.NewHTTPVendorClient(cfg.VendorServiceURL, internalServices.Key)
	orderClient := adapter.NewHTTPOrderClient(cfg.OrderServiceURL)
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

	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, categoryHandler, productHandler, storefrontHandler, adminHandler, internalHandler, attributeHandler, internalServices.Key,
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

	maintenance := transport.MaintenanceHandler{Service: usecase.Maintenance{Repository: repository.Maintenance{Pool: dbPool}, Identity: identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}}, Log: log}
	router.GET("/api/catalog/operations", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin"), maintenance.Stats)
	router.POST("/api/catalog/operations/replay", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin"), maintenance.Replay)
	router.POST("/internal/stock-cache/invalidate", serviceauth.Require(internalServices.Key, serviceauth.Header), maintenance.InvalidateStock)
	salesStore := vendorsales.Store{Pool: dbPool}
	router.POST("/internal/vendor-status", serviceauth.Require(internalServices.Key, serviceauth.Header), salesStore.Handler(log))
	reconcileCtx, stopReconcile := context.WithCancel(ctx)
	defer stopReconcile()
	go productUseCase.ReconcileCache(reconcileCtx, storefrontCacheRepo)
	go (repository.ObjectCleanup{Pool: dbPool}).Run(reconcileCtx, objectStore, log)
	go (repository.StatusOutbox{Pool: dbPool, Publish: (adapter.ProductStatusPublisher{URL: cfg.OrderServiceURL, Key: internalServices.Key}).Publish}).Run(reconcileCtx, log)
	go (vendorsales.Client{URL: cfg.VendorServiceURL, Key: internalServices.Key}).Reconcile(reconcileCtx, salesStore, log)

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
