// Command server runs the order service.
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
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/platform/redisclient"
	"shopee/backend/pkg/shutdown"

	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/config"
	"shopee/backend/services/order/internal/repository"
	"shopee/backend/services/order/internal/transport"
	"shopee/backend/services/order/internal/usecase"
)

const serviceName = "order"

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
	cartClient := adapter.NewHTTPCartClient(cfg.CartServiceURL)
	catalogClient := adapter.NewHTTPCatalogClient(cfg.CatalogServiceURL)
	vendorClient := adapter.NewHTTPVendorClient(cfg.VendorServiceURL)
	inventoryClient := adapter.NewHTTPInventoryClient(cfg.InventoryServiceURL)
	shipmentClient := adapter.NewHTTPShipmentClient(cfg.ShipmentServiceURL)
	notificationClient := adapter.NewHTTPNotificationClient(cfg.NotificationServiceURL)

	orderRepo := repository.NewOrderRepository(dbPool)
	vendorOrderRepo := repository.NewVendorOrderRepository(dbPool)
	buyerAddressRepo := repository.NewBuyerAddressRepository(dbPool)
	commissionRuleRepo := repository.NewCommissionRuleRepository(dbPool)

	orderUseCase := usecase.NewOrderUseCase(
		orderRepo, vendorOrderRepo, buyerAddressRepo, commissionRuleRepo,
		cartClient, catalogClient, vendorClient, inventoryClient, shipmentClient, notificationClient, log,
	)
	orderHandler := transport.NewOrderHandler(orderUseCase, log)
	addressHandler := transport.NewBuyerAddressHandler(orderUseCase, log)
	adminHandler := transport.NewAdminHandler(orderUseCase, log)
	internalHandler := transport.NewInternalHandler(orderUseCase, log)

	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, orderHandler, addressHandler, adminHandler, internalHandler,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return dbPool.Ping(ctx) }},
		health.Checker{Name: "redis", Ping: func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }},
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
