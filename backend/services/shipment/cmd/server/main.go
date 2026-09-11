// Command server runs the shipment service.
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

	"shopee/backend/services/shipment/internal/adapter"
	"shopee/backend/services/shipment/internal/carrier/mock"
	"shopee/backend/services/shipment/internal/config"
	"shopee/backend/services/shipment/internal/repository"
	"shopee/backend/services/shipment/internal/transport"
	"shopee/backend/services/shipment/internal/usecase"
)

const serviceName = "shipment"

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
	vendorClient := adapter.NewHTTPVendorClient(cfg.VendorServiceURL)
	orderClient := adapter.NewHTTPOrderClient(cfg.OrderServiceURL)

	shipmentRepo := repository.NewShipmentRepository(dbPool)
	carrierRepo := repository.NewCarrierRepository(dbPool)
	zoneRepo := repository.NewZoneRepository(dbPool)
	feeRuleRepo := repository.NewFeeRuleRepository(dbPool)
	vendorMethodRepo := repository.NewVendorShippingMethodRepository(dbPool)
	trackingEventRepo := repository.NewTrackingEventRepository(dbPool)

	// SHIPMENT_CARRIER_PROVIDER is validated in config.Load, so "mock" is
	// the only value reaching here today; a real carrier adapter is wired
	// in the same way once this deployment has real carrier credentials.
	mockCarrier := mock.New(cfg.CarrierMockWebhookSecret)

	shipmentUseCase := usecase.NewShipmentUseCase(
		shipmentRepo, vendorMethodRepo, zoneRepo, feeRuleRepo, trackingEventRepo, vendorClient, orderClient,
		mockCarrier, mockCarrier, mockCarrier, log,
	)
	carrierUseCase := usecase.NewCarrierUseCase(carrierRepo)
	zoneUseCase := usecase.NewZoneUseCase(zoneRepo)
	feeRuleUseCase := usecase.NewFeeRuleUseCase(feeRuleRepo, carrierRepo, zoneRepo)
	vendorMethodUseCase := usecase.NewVendorShippingMethodUseCase(vendorMethodRepo, carrierRepo, vendorClient)

	shipmentHandler := transport.NewShipmentHandler(shipmentUseCase, log)
	adminHandler := transport.NewAdminHandler(carrierUseCase, zoneUseCase, feeRuleUseCase, log)
	vendorMethodHandler := transport.NewVendorShippingMethodHandler(vendorMethodUseCase, log)
	internalHandler := transport.NewInternalHandler(shipmentUseCase, log)
	webhookHandler := transport.NewWebhookHandler(shipmentUseCase, log)

	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, shipmentHandler, adminHandler, vendorMethodHandler, internalHandler, webhookHandler,
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
