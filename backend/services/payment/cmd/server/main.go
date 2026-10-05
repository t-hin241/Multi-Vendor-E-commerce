// Command server runs the payment service.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"shopee/backend/pkg/adminaudit"
	"shopee/backend/pkg/authjwt"
	sessionconfig "shopee/backend/pkg/config"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/identityclient"
	"shopee/backend/pkg/logger"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/platform/redisclient"
	"shopee/backend/pkg/shutdown"
	"shopee/backend/pkg/telemetry"
	"shopee/backend/pkg/vendorreport"
	"shopee/backend/services/payment/internal/adapter"
	"shopee/backend/services/payment/internal/config"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/provider"
	"shopee/backend/services/payment/internal/provider/mock"
	"shopee/backend/services/payment/internal/provider/payos"
	"shopee/backend/services/payment/internal/repository"
	"shopee/backend/services/payment/internal/transport"
	"shopee/backend/services/payment/internal/usecase"
)

const serviceName = "payment"

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, serviceName+": config error:", err)
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

	redisClient, err := redisclient.NewClient(ctx, cfg.Base.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("redis connection failed")
	}
	if err := telemetry.RegisterRedisPool(redisClient); err != nil {
		log.Fatal().Err(err).Msg("redis pool metrics failed")
	}
	defer redisClient.Close()

	busPassword, err := sessionconfig.RequireEventBusPassword()
	if err != nil {
		log.Fatal().Err(err).Msg("event bus configuration invalid")
	}
	bus, err := eventbus.Start(cfg.Base.NATSURL, eventbus.Credentials{Service: "payment", Password: busPassword}, cfg.Base.EventPublishing, dbPool, log)
	if err != nil {
		log.Fatal().Err(err).Msg("event bus configuration invalid")
	}

	jwtManager := authjwt.NewManager(cfg.JWTSecret)
	sessionVerifier, err := sessionconfig.LoadSessionVerifier()
	if err != nil {
		log.Fatal().Err(err).Msg("session verifier configuration invalid")
	}
	jwtManager.SetVerifier(sessionVerifier)
	internalServices, err := sessionconfig.LoadInternalServices()
	if err != nil {
		log.Fatal().Msg("internal service configuration invalid")
	}
	orderClient := adapter.NewHTTPOrderClient(cfg.OrderServiceURL, internalServices.Key)
	vendorClient := adapter.NewHTTPVendorClient(cfg.VendorServiceURL, internalServices.Key)
	roles := identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}

	var paymentProvider provider.Provider
	var verifier provider.Verifier
	var simulator usecase.Simulator
	if cfg.Provider == "payos" {
		p := payos.New(cfg.PayOSClientID, cfg.PayOSAPIKey, cfg.PayOSChecksumKey, cfg.PayOSBaseURL)
		paymentProvider, verifier = p, p
	} else {
		p := mock.New(cfg.MockWebhookSecret)
		paymentProvider, verifier, simulator = p, p, p
	}

	tx := repository.Transactions{Pool: dbPool}
	intentRepo := repository.NewPaymentIntentRepository(dbPool)
	receiptRepo := repository.NewReceiptRepository(dbPool)
	orderSync := repository.OrderSync{Pool: dbPool}
	refundSync := repository.RefundSync{Pool: dbPool}

	// PLT-03: outcomes go to Order through the event bus (payment.outcome,
	// payment.refund_outcome); Order's refusal comes back as
	// order.payment_outcome_rejected and puts the outcome up for review.
	deliverOutcome := func(ctx context.Context, out repository.OrderOutcome) error {
		if bus.Publish {
			env, err := events.PaymentOutcomeEvent(events.PaymentResult{PaymentID: out.PaymentID, OrderID: out.OrderID, Outcome: out.Outcome,
				Amount: out.Amount, Currency: out.Currency, Reason: out.Reason})
			if err != nil {
				return err
			}
			return bus.Bus.Publish(ctx, env.WithCorrelation(""))
		}
		if out.Outcome == "captured" {
			return orderClient.MarkPaid(ctx, out.OrderID, adapter.Capture{PaymentID: out.PaymentID, Amount: out.Amount, Currency: out.Currency})
		}
		reason := out.Reason
		if reason == "" {
			reason = "Payment failed"
		}
		return orderClient.MarkPaymentFailed(ctx, out.OrderID, reason)
	}
	wakeSync := make(chan struct{}, 1)
	syncNow := func(ctx context.Context, intentID string) {
		// Deliver right away; on any failure the outbox worker retries.
		err := orderSync.DispatchIntent(context.WithoutCancel(ctx), intentID, deliverOutcome)
		if err != nil && !errors.Is(err, repository.ErrNoOrderSync) {
			select {
			case wakeSync <- struct{}{}:
			default:
			}
		}
	}

	paymentUseCase := usecase.NewPaymentUseCase(usecase.PaymentDeps{
		Tx: tx, Intents: intentRepo, Receipts: receiptRepo, Orders: orderClient, Provider: paymentProvider, Verifier: verifier,
		Simulator: simulator, ProviderName: cfg.Provider, ReturnURL: cfg.PayOSReturnURL, CancelURL: cfg.PayOSCancelURL, SyncNow: syncNow, Log: log,
	})
	settlementUseCase := usecase.NewSettlementUseCase(usecase.SettlementDeps{
		Tx: tx, Settlement: repository.NewSettlementRepository(dbPool), Payouts: repository.NewPayoutRepository(dbPool),
		Audit: repository.NewAuditRepository(dbPool), Roles: roles, Orders: orderClient, Vendors: vendorClient, Log: log,
	})
	refundRepo := repository.NewRefundRepository(dbPool)
	refundUseCase := usecase.NewRefundUseCase(refundRepo, roles, log).WithSettlement(tx, settlementUseCase)
	reconUseCase := usecase.NewReconciliationUseCase(usecase.ReconciliationDeps{
		Tx: tx, Payments: paymentUseCase, Refunds: refundUseCase, Intents: intentRepo, Receipts: receiptRepo,
		OrderSync: orderSync, RefundSync: refundSync, RefundStore: refundRepo, Audit: repository.NewAuditRepository(dbPool), Roles: roles, Log: log,
	})
	limiter := adapter.RedisRateLimiter{Client: redisClient, Prefix: "payment:webhook:", Limit: cfg.WebhookRatePerMinute, Window: time.Minute}

	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, transport.Handlers{
		Payment: transport.NewPaymentHandler(paymentUseCase, log),
		Webhook: transport.NewWebhookHandler(paymentUseCase, limiter, log),
		Refund:  transport.NewRefundHandler(refundUseCase, log),
		Admin:   transport.NewAdminHandler(reconUseCase, settlementUseCase, log),
	}, internalServices.Verifier,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return dbPool.Ping(ctx) }},
		health.Checker{Name: "redis", Ping: func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }},
	)

	router.GET("/internal/vendor-reports/:vendorId", internalServices.Verifier.Allow("vendor"), vendorreport.Handler(vendorreport.Service{Repository: repository.VendorReport{Pool: dbPool}}, log))

	syncCtx, stopSync := context.WithCancel(ctx)
	defer stopSync()
	go orderSync.Run(syncCtx, deliverOutcome, log, wakeSync)
	deliverRefund := orderClient.ReportRefund
	if bus.Publish {
		deliverRefund = func(ctx context.Context, out domain.RefundOutcome) error {
			env, err := events.RefundOutcomeEvent(events.RefundResult{OrderRefundID: out.OrderRefundID, PaymentRefundID: out.PaymentRefundID,
				Status: out.Status, Amount: out.Amount, Currency: out.Currency, FailureReason: out.FailureReason})
			if err != nil {
				return err
			}
			return bus.Bus.Publish(ctx, env.WithCorrelation(""))
		}
	}
	go refundSync.Run(syncCtx, deliverRefund, log)
	bus.Run(syncCtx,
		eventbus.Subscription{Durable: "payment-settlements", Types: []string{events.VendorOrderSettleable}, Handle: transport.SettleableHandler(settlementUseCase)},
		eventbus.Subscription{Durable: "payment-rejected-outcomes", Types: []string{events.PaymentOutcomeRejected}, Handle: transport.OutcomeRejectedHandler(orderSync, refundSync)},
	)
	go (usecase.PaymentWorker{Payments: paymentUseCase, Reconciliation: reconUseCase, Log: log}).Run(syncCtx)
	adminGroup := router.Group("/api/payments/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin"))
	bus.RegisterAdmin(adminGroup, roles)
	adminaudit.Register(adminGroup, "/audit-events",
		adminaudit.Source{Name: "payment", SQL: repository.AuditSearchSQL + " UNION ALL " + eventbus.InboxAuditSearchSQL, DB: dbPool, Roles: roles}, log)

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
		stopSync()
		bus.Close(cfg.Base.ShutdownTimeout)
	})
}
