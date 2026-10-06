// Command server runs the order service.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"shopee/backend/pkg/adminaudit"
	"shopee/backend/pkg/middleware"

	sessionconfig "shopee/backend/pkg/config"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/identityclient"
	"shopee/backend/pkg/logger"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/productsales"
	"shopee/backend/pkg/shutdown"
	"shopee/backend/pkg/telemetry"
	"shopee/backend/pkg/vendorreport"
	"shopee/backend/pkg/vendorsales"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/config"
	"shopee/backend/services/order/internal/domain"
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
	bus, err := eventbus.Start(cfg.Base.NATSURL, eventbus.Credentials{Service: "order", Password: busPassword}, cfg.Base.EventPublishing, dbPool, log)
	if err != nil {
		log.Fatal().Err(err).Msg("event bus configuration invalid")
	}
	var eventPublisher usecase.EventPublisher
	if bus.Publish {
		eventPublisher = bus.Bus
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
	cartClient := adapter.NewHTTPCartClient(cfg.CartServiceURL, internalServices.Key)
	catalogClient := adapter.NewHTTPCatalogClient(cfg.CatalogServiceURL, internalServices.Key)
	vendorClient := adapter.NewHTTPVendorClient(cfg.VendorServiceURL, internalServices.Key)
	inventoryClient := adapter.NewHTTPInventoryClient(cfg.InventoryServiceURL, internalServices.Key)
	shipmentClient := adapter.NewHTTPShipmentClient(cfg.ShipmentServiceURL, internalServices.Key)
	notificationClient := adapter.NewHTTPNotificationClient(cfg.NotificationServiceURL, internalServices.Key)
	paymentClient := adapter.NewHTTPPaymentClient(cfg.PaymentServiceURL, internalServices.Key)

	vendorOrderRepo := repository.NewVendorOrderRepository(dbPool)
	orderUseCase := usecase.NewOrderUseCase(usecase.Deps{
		Events:          eventPublisher,
		Orders:          repository.NewOrderRepository(dbPool),
		VendorOrders:    vendorOrderRepo,
		BuyerAddresses:  repository.NewBuyerAddressRepository(dbPool),
		CommissionRules: repository.NewCommissionRuleRepository(dbPool),
		CartConsumption: repository.NewCartConsumptionRepository(dbPool),
		CheckoutOps:     repository.NewCheckoutOperationRepository(dbPool),
		Payments:        repository.NewPaymentRecordRepository(dbPool),
		Effects:         repository.NewEffectRepository(dbPool),
		Refunds:         repository.NewRefundRepository(dbPool),
		Returns:         repository.NewReturnRequestRepository(dbPool),
		Cart:            cartClient,
		Catalog:         catalogClient,
		Vendors:         vendorClient,
		Inventory:       inventoryClient,
		Shipments:       shipmentClient,
		Notifications:   notificationClient,
		Payment:         paymentClient,
		Identity:        identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key},
		Audit:           repository.NewAuditRepository(dbPool),
		Tx:              repository.Transactions{Pool: dbPool},
		Operations:      repository.Operations{Pool: dbPool},
		ReturnPolicy:    domain.ReturnPolicy{Version: cfg.ReturnPolicyVersion(), WindowDays: cfg.ReturnWindowDays},
		Log:             log,
	})
	workerCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	go usecase.OrderWorker{UseCase: orderUseCase}.Run(workerCtx)
	orderHandler := transport.NewOrderHandler(orderUseCase, log)
	addressHandler := transport.NewBuyerAddressHandler(orderUseCase, log)
	adminHandler := transport.NewAdminHandler(orderUseCase, log)
	internalHandler := transport.NewInternalHandler(orderUseCase, log)
	returnHandler := transport.NewReturnHandler(orderUseCase, log)
	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, orderHandler, addressHandler, adminHandler, internalHandler, returnHandler,
		internalServices.Verifier,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return dbPool.Ping(ctx) }},
	)

	salesStore := vendorsales.Store{Pool: dbPool}
	router.POST("/internal/vendor-status", internalServices.Verifier.Allow("vendor"), salesStore.Handler(log))
	router.POST("/internal/product-status", internalServices.Verifier.Allow("catalog"), (productsales.Store{Pool: dbPool}).Handler(log))
	reconcileCtx, stopReconcile := context.WithCancel(ctx)
	defer stopReconcile()
	go (vendorsales.Client{URL: cfg.VendorServiceURL, Key: internalServices.Key}).Reconcile(reconcileCtx, salesStore, log)
	// PLT-03: facts from other services arrive from the event bus; the
	// internal HTTP routes stay for producers in rollback mode.
	bus.Run(reconcileCtx,
		eventbus.Subscription{Durable: "order-vendor-status", Types: []string{events.VendorStatusChanged}, Handle: salesStore.EventHandler()},
		eventbus.Subscription{Durable: "order-product-status", Types: []string{events.ProductStatusChanged}, Handle: (productsales.Store{Pool: dbPool}).EventHandler()},
		eventbus.Subscription{Durable: "order-reservation-expiry", Types: []string{events.ReservationExpired}, Handle: transport.ReservationExpiredHandler(orderUseCase)},
		eventbus.Subscription{Durable: "order-shipment-facts", Types: []string{events.ShipmentChanged}, Handle: transport.ShipmentChangedHandler(orderUseCase)},
		eventbus.Subscription{Durable: "order-payment-outcomes", Types: []string{events.PaymentOutcome}, Handle: transport.PaymentOutcomeHandler(orderUseCase)},
		eventbus.Subscription{Durable: "order-refund-outcomes", Types: []string{events.RefundOutcome}, Handle: transport.RefundOutcomeHandler(orderUseCase)},
	)

	router.GET("/internal/vendor-reports/:vendorId", internalServices.Verifier.Allow("vendor"), vendorreport.Handler(vendorreport.Service{Repository: vendorOrderRepo}, log))

	adminGroup := router.Group("/api/orders/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin"))
	bus.RegisterAdmin(adminGroup, identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key})
	adminaudit.Register(adminGroup, "/audit-events",
		adminaudit.Source{Name: "order", SQL: repository.AuditSearchSQL + " UNION ALL " + eventbus.InboxAuditSearchSQL, DB: dbPool, Roles: identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}}, log)
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
		stopWorker()
		bus.Close(cfg.Base.ShutdownTimeout)
	})
}
