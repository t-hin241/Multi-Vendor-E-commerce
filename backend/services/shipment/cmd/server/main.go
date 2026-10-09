// Command server runs the shipment service.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"shopee/backend/pkg/casesla"

	"shopee/backend/pkg/adminaudit"
	"shopee/backend/pkg/middleware"

	"shopee/backend/pkg/adminaccess"
	sessionconfig "shopee/backend/pkg/config"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/identityclient"
	"shopee/backend/pkg/logger"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/shutdown"
	"shopee/backend/pkg/telemetry"
	"shopee/backend/services/shipment/internal/adapter"
	"shopee/backend/services/shipment/internal/carrier/manual"
	"shopee/backend/services/shipment/internal/carrier/mock"
	"shopee/backend/services/shipment/internal/config"
	"shopee/backend/services/shipment/internal/domain"
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

	slaConfig, err := sessionconfig.LoadCaseSLA()
	if err != nil {
		fmt.Fprintln(os.Stderr, "case SLA configuration invalid:", err)
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
	bus, err := eventbus.Start(cfg.Base.NATSURL, eventbus.Credentials{Service: "shipment", Password: busPassword}, cfg.Base.EventPublishing, dbPool, log)
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
	orderClient := adapter.NewHTTPOrderClient(cfg.OrderServiceURL, internalServices.Key)

	shipmentRepo := repository.NewShipmentRepository(dbPool)
	carrierRepo := repository.NewCarrierRepository(dbPool)
	zoneRepo := repository.NewZoneRepository(dbPool)
	feeRuleRepo := repository.NewFeeRuleRepository(dbPool)
	vendorMethodRepo := repository.NewVendorShippingMethodRepository(dbPool)
	outbox := repository.OrderOutbox{Pool: dbPool}

	deps := usecase.Deps{
		Tx: repository.Transactions{Pool: dbPool}, Shipments: shipmentRepo, VendorMethods: vendorMethodRepo, Carriers: carrierRepo, Zones: zoneRepo,
		FeeRules: feeRuleRepo, Events: repository.NewTrackingEventRepository(dbPool), Outbox: outbox,
		Vendors: vendorClient, Orders: orderClient, Identity: identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key},
		Audit: repository.AuditRepository{Pool: dbPool},
		Log:   log,
		// AF-04: failed deliveries become Order cases; redelivery attempts.
		DeliveryResolution: cfg.DeliveryResolution, AttemptLimit: cfg.AttemptLimit,
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
	adminConfig := usecase.AdminConfig{Tx: repository.Transactions{Pool: dbPool}, Identity: identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key},
		Audit: repository.AuditRepository{Pool: dbPool}}
	carrierUseCase := usecase.NewCarrierUseCase(carrierRepo, adminConfig)
	zoneUseCase := usecase.NewZoneUseCase(zoneRepo, adminConfig)
	feeRuleUseCase := usecase.NewFeeRuleUseCase(feeRuleRepo, carrierRepo, zoneRepo, adminConfig)
	vendorMethodUseCase := usecase.NewVendorShippingMethodUseCase(vendorMethodRepo, carrierRepo, vendorClient)

	// AF-19: every admin route needs the bundle named in transport.AdminRoutes.
	adminGuard := adminaccess.Guard(adminaccess.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}, transport.AdminRoutes, log)
	router := transport.NewRouter(cfg.Base.Env, log, jwtManager,
		transport.NewShipmentHandler(shipmentUseCase, log),
		transport.NewAdminHandler(carrierUseCase, zoneUseCase, feeRuleUseCase, log),
		transport.NewVendorShippingMethodHandler(vendorMethodUseCase, log),
		transport.NewInternalHandler(shipmentUseCase, log),
		transport.NewWebhookHandler(shipmentUseCase, log),
		transport.NewOpsHandler(shipmentUseCase, log),
		adminGuard,
		internalServices.Verifier,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return dbPool.Ping(ctx) }},
	)

	workerCtx, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()
	// PLT-03: shipment facts go to Order through the event bus (shipment.
	// status_changed; HTTP only in rollback mode). Order's refusal parks the
	// event in Order's inbox, where it is replayed or discarded.
	go outbox.Run(workerCtx, func(ctx context.Context, e repository.OutboxEvent) error {
		// AF-04: exception facts have their own event type and HTTP route.
		if kind, ok := domain.ExceptionTypeOf(e.Type); ok {
			x := events.ShipmentException{EventID: e.ID, ShipmentID: e.ShipmentID, VendorOrderID: e.VendorOrderID, ExceptionType: string(kind),
				OccurredAt: e.OccurredAt}
			if e.AttemptNo != nil {
				x.AttemptNo = *e.AttemptNo
			}
			if e.FailedAttempts != nil {
				x.FailedAttempts = *e.FailedAttempts
			}
			if e.Reason != nil {
				x.Reason = *e.Reason
			}
			if !bus.Publish {
				return orderClient.SendShipmentException(ctx, x)
			}
			env, err := events.ShipmentExceptionEvent(x)
			if err != nil {
				return err
			}
			return bus.Bus.Publish(ctx, env.WithCorrelation(e.ID))
		}
		if !bus.Publish {
			return orderClient.SendShipmentEvent(ctx, adapter.ShipmentEvent{EventID: e.ID, ShipmentID: e.ShipmentID, VendorOrderID: e.VendorOrderID,
				Type: string(e.Type), OccurredAt: e.OccurredAt, TrackingNumber: e.TrackingNumber})
		}
		env, err := events.ShipmentChangedEvent(events.ShipmentFact{EventID: e.ID, ShipmentID: e.ShipmentID, VendorOrderID: e.VendorOrderID,
			Type: string(e.Type), OccurredAt: e.OccurredAt, TrackingNumber: e.TrackingNumber})
		if err != nil {
			return err
		}
		return bus.Bus.Publish(ctx, env.WithCorrelation(e.ID))
	}, log, wake)
	bus.Run(workerCtx,
		eventbus.Subscription{Durable: "shipment-fulfillment-ready", Types: []string{events.FulfillmentReady}, Handle: transport.FulfillmentReadyHandler(shipmentUseCase)},
		eventbus.Subscription{Durable: "shipment-fulfillment-cancelled", Types: []string{events.FulfillmentCancelled}, Handle: transport.FulfillmentCancelledHandler(shipmentUseCase)},
	)
	go (usecase.Worker{Shipments: shipmentUseCase, Retention: cfg.AddressRetention, Log: log}).Run(workerCtx)

	adminGroup := router.Group("/api/shipments/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin"), adminGuard)
	slaRoles := identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}
	slaStore := casesla.Store{Pool: dbPool}
	casesla.Register(adminGroup, casesla.Service{Repo: slaStore, Roles: slaRoles, Permissions: adminaccess.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}}, log)
	go (casesla.Worker{Store: slaStore, Owner: "shipment", Config: slaConfig, Roles: slaRoles, Permissions: adminaccess.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}, Publisher: bus.Bus, Log: log}).Run(workerCtx)
	bus.RegisterAdmin(adminGroup, identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key})
	adminaudit.Register(adminGroup, "/audit-events",
		adminaudit.Source{Name: "shipment", SQL: repository.AuditSearchSQL + " UNION ALL " + eventbus.InboxAuditSearchSQL + " UNION ALL " + casesla.AuditSearchSQL, DB: dbPool, Roles: identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}}, log)
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
		stopWorkers()
		bus.Close(cfg.Base.ShutdownTimeout)
	})
}
