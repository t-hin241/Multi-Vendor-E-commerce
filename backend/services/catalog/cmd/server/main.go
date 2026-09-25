// Command server runs the catalog service.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/logger"
	"shopee/backend/pkg/platform/natsclient"
	"shopee/backend/pkg/platform/objectstorage"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/platform/redisclient"
	"shopee/backend/pkg/shutdown"

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
	vendorClient := adapter.NewHTTPVendorClient(cfg.VendorServiceURL)
	orderClient := adapter.NewHTTPOrderClient(cfg.OrderServiceURL)
	inventoryClient := adapter.NewHTTPInventoryClient(cfg.InventoryServiceURL)

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

	categoryUseCase := usecase.NewCategoryUseCase(categoryRepo)
	attributeUseCase := usecase.NewAttributeUseCase(attributeRepo, categoryAttributeRuleRepo, categoryRepo)
	productUseCase := usecase.NewProductUseCase(
		productRepo, imageRepo, mediaRepo, categoryRepo, auditLogRepo, vendorClient, objectStore,
		vendorClient, orderClient, storefrontCacheRepo, attributeUseCase, productAttributeValueRepo, productVariantRepo,
		inventoryClient, productPackagingRepo,
	)

	categoryHandler := transport.NewCategoryHandler(categoryUseCase, log)
	productHandler := transport.NewProductHandler(productUseCase, log)
	storefrontHandler := transport.NewStorefrontHandler(productUseCase, log)
	adminHandler := transport.NewAdminHandler(productUseCase, log)
	internalHandler := transport.NewInternalHandler(productUseCase, log)
	attributeHandler := transport.NewAttributeHandler(attributeUseCase, log)

	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, categoryHandler, productHandler, storefrontHandler, adminHandler, internalHandler, attributeHandler,
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
