// Command server runs the shipment service.
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
	"shopee/backend/pkg/platform/natsclient"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/platform/redisclient"
	"shopee/backend/pkg/shutdown"
	"shopee/backend/services/shipment/internal/adapter"
	"shopee/backend/services/shipment/internal/carrier/manual"
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
	orderClient := adapter.NewHTTPOrderClient(cfg.OrderServiceURL, internalServices.Key)

	shipmentRepo := repository.NewShipmentRepository(dbPool)
	carrierRepo := repository.NewCarrierRepository(dbPool)
	zoneRepo := repository.NewZoneRepository(dbPool)
	feeRuleRepo := repository.NewFeeRuleRepository(dbPool)
	vendorMethodRepo := repository.NewVendorShippingMethodRepository(dbPool)
	outbox := repository.OrderOutbox{Pool: dbPool}

	deps := usecase.Deps{
		Tx: repository.Transactions{Pool: dbPool}, Shipments: shipmentRepo, VendorMethods: vendorMethodRepo, Zones: zoneRepo,
		FeeRules: feeRuleRepo, Events: repository.NewTrackingEventRepository(dbPool), Outbox: outbox,
		Vendors: vendorClient, Orders: orderClient, Identity: identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key},
		Log: log,
	}
	if cfg.CarrierProvider == "mock" {
		mockCarrier := mock.New(cfg.CarrierMockWebhookSecret)
		deps.Carrier, deps.Verifier, deps.Simulator = mockCarrier, mockCarrier, mockCarrier
	} else {
		deps.Carrier, deps.Verifier = manual.Provider{}, manual.Provider{}
	}
	wake := make(chan struct{}, 1)
	deps.Wake = func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	shipmentUseCase := usecase.NewShipmentUseCase(deps)
	carrierUseCase := usecase.NewCarrierUseCase(carrierRepo)
	zoneUseCase := usecase.NewZoneUseCase(zoneRepo)
	feeRuleUseCase := usecase.NewFeeRuleUseCase(feeRuleRepo, carrierRepo, zoneRepo)
	vendorMethodUseCase := usecase.NewVendorShippingMethodUseCase(vendorMethodRepo, carrierRepo, vendorClient)

	router := transport.NewRouter(cfg.Base.Env, log, jwtManager,
		transport.NewShipmentHandler(shipmentUseCase, log),
		transport.NewAdminHandler(carrierUseCase, zoneUseCase, feeRuleUseCase, log),
		transport.NewVendorShippingMethodHandler(vendorMethodUseCase, log),
		transport.NewInternalHandler(shipmentUseCase, log),
		transport.NewWebhookHandler(shipmentUseCase, log),
		transport.NewOpsHandler(shipmentUseCase, log),
		internalServices.Key,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return dbPool.Ping(ctx) }},
		health.Checker{Name: "redis", Ping: func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }},
		health.Checker{Name: "nats", Ping: func(ctx context.Context) error {
			if !natsConn.IsConnected() {
				return fmt.Errorf("nats: not connected")
			}
			return nil
		}},
	)

	workerCtx, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()
	go outbox.Run(workerCtx, func(ctx context.Context, e repository.OutboxEvent) error {
		return orderClient.SendShipmentEvent(ctx, adapter.ShipmentEvent{EventID: e.ID, ShipmentID: e.ShipmentID, VendorOrderID: e.VendorOrderID,
			Type: string(e.Type), OccurredAt: e.OccurredAt, TrackingNumber: e.TrackingNumber})
	}, log, wake)
	go (usecase.Worker{Shipments: shipmentUseCase, Retention: cfg.AddressRetention, Log: log}).Run(workerCtx)

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
