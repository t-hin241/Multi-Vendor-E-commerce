// Command server runs the order service.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"shopee/backend/pkg/adminaudit"
	"shopee/backend/pkg/middleware"

	"shopee/backend/pkg/authjwt"
	sessionconfig "shopee/backend/pkg/config"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/identityclient"
	"shopee/backend/pkg/logger"
	"shopee/backend/pkg/platform/natsclient"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/platform/redisclient"
	"shopee/backend/pkg/productsales"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/shutdown"
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
	cartClient := adapter.NewHTTPCartClient(cfg.CartServiceURL, internalServices.Key)
	catalogClient := adapter.NewHTTPCatalogClient(cfg.CatalogServiceURL, internalServices.Key)
	vendorClient := adapter.NewHTTPVendorClient(cfg.VendorServiceURL, internalServices.Key)
	inventoryClient := adapter.NewHTTPInventoryClient(cfg.InventoryServiceURL, internalServices.Key)
	shipmentClient := adapter.NewHTTPShipmentClient(cfg.ShipmentServiceURL, internalServices.Key)
	notificationClient := adapter.NewHTTPNotificationClient(cfg.NotificationServiceURL, internalServices.Key)
	paymentClient := adapter.NewHTTPPaymentClient(cfg.PaymentServiceURL, internalServices.Key)

	vendorOrderRepo := repository.NewVendorOrderRepository(dbPool)
	orderUseCase := usecase.NewOrderUseCase(usecase.Deps{
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

	salesStore := vendorsales.Store{Pool: dbPool}
	router.POST("/internal/vendor-status", serviceauth.Require(internalServices.Key, serviceauth.Header), salesStore.Handler(log))
	router.POST("/internal/product-status", serviceauth.Require(internalServices.Key, serviceauth.Header), (productsales.Store{Pool: dbPool}).Handler(log))
	reconcileCtx, stopReconcile := context.WithCancel(ctx)
	defer stopReconcile()
	go (vendorsales.Client{URL: cfg.VendorServiceURL, Key: internalServices.Key}).Reconcile(reconcileCtx, salesStore, log)

	router.GET("/internal/vendor-reports/:vendorId", serviceauth.Require(internalServices.Key, serviceauth.Header), vendorreport.Handler(vendorreport.Service{Repository: vendorOrderRepo}, log))

	router.GET("/internal/orders/:id/inventory-status", serviceauth.Require(internalServices.Key, serviceauth.Header), internalHandler.InventoryStatus)
	router.POST("/internal/inventory-events", serviceauth.Require(internalServices.Key, serviceauth.Header), internalHandler.InventoryEvent)
	adminaudit.Register(router.Group("/api/orders/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin")), "/audit-events",
		adminaudit.Source{Name: "order", SQL: repository.AuditSearchSQL, DB: dbPool, Roles: identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}}, log)
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
