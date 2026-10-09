// Command server runs the payment service.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"shopee/backend/pkg/casesla"
	"time"

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

	jwtManager, err := sessionconfig.LoadTokenVerifier()
	if err != nil {
		log.Fatal().Err(err).Msg("access token verifier configuration invalid")
	}
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
		RequireApprovals: cfg.AdminApprovals, SkipOrderHoldQuery: cfg.SkipOrderHoldQuery,
	})
	if cfg.SkipOrderHoldQuery {
		log.Warn().Msg("settlement_order_hold_query_off")
	}
	// AF-08: payout results for the shop, relayed to the event bus.
	vendorNotices := repository.VendorNotices{Pool: dbPool}
	if cfg.VendorActionNotices {
		settlementUseCase.VendorNotices = vendorNotices
	}
	refundRepo := repository.NewRefundRepository(dbPool)
	refundUseCase := usecase.NewRefundUseCase(refundRepo, roles, log).WithSettlement(tx, settlementUseCase).RequireApprovals(cfg.AdminApprovals)
	// AF-19: scoped admin permissions (Identity) and maker-checker requests.
	admins := adminaccess.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}
	adminGuard := adminaccess.Guard(admins, transport.AdminRoutes, log)
	approvalUseCase := &usecase.ApprovalUseCase{Store: repository.ApprovalRepository{Pool: dbPool}, Refunds: refundUseCase, Settlement: settlementUseCase,
		Payouts: repository.NewPayoutRepository(dbPool), RefundsRepo: refundRepo, Audit: repository.NewAuditRepository(dbPool), Tx: tx, Admins: admins,
		Enabled: cfg.AdminApprovals, Log: log}
	// AF-06: manual bank-transfer refunds. Two-person review and password
	// proofs follow the AF-19 flag.
	manualUseCase := &usecase.ManualRefundUseCase{Store: repository.ManualRefundRepository{Pool: dbPool}, Refunds: refundUseCase,
		Audit: repository.NewAuditRepository(dbPool), Tx: tx, Admins: admins, Enabled: cfg.ManualRefunds.Enabled,
		StepUp: cfg.AdminApprovals, TwoPerson: cfg.AdminApprovals, Lease: cfg.ManualRefunds.Lease, Log: log}
	if len(cfg.ManualRefunds.Keys) > 0 {
		destinationCipher, err := adapter.NewDestinationCipher(cfg.ManualRefunds.KeyVersion, cfg.ManualRefunds.Keys)
		if err != nil {
			log.Fatal().Err(err).Msg("refund destination key invalid")
		}
		manualUseCase.Cipher = destinationCipher
	} else {
		log.Warn().Msg("refund_destination_key_missing")
	}
	if cfg.ManualRefunds.Evidence != nil {
		storeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		evidenceStore, err := objectstorage.NewPrivateClient(storeCtx, *cfg.ManualRefunds.Evidence)
		cancel()
		if err != nil {
			log.Fatal().Err(err).Msg("refund evidence storage unavailable")
		}
		manualUseCase.Evidence = evidenceStore
	}
	refundUseCase.WithLegacyGuard(manualUseCase.GuardLegacyResolution)
	reconUseCase := usecase.NewReconciliationUseCase(usecase.ReconciliationDeps{
		Tx: tx, Payments: paymentUseCase, Refunds: refundUseCase, Intents: intentRepo, Receipts: receiptRepo,
		OrderSync: orderSync, RefundSync: refundSync, RefundStore: refundRepo, Audit: repository.NewAuditRepository(dbPool), Roles: roles, Log: log,
	})
	limiter := adapter.RedisRateLimiter{Client: redisClient, Prefix: "payment:webhook:", Limit: cfg.WebhookRatePerMinute, Window: time.Minute}

	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, transport.Handlers{
		Payment:  transport.NewPaymentHandler(paymentUseCase, log),
		Webhook:  transport.NewWebhookHandler(paymentUseCase, limiter, log),
		Refund:   transport.NewRefundHandler(refundUseCase, log),
		Admin:    transport.NewAdminHandler(reconUseCase, settlementUseCase, log),
		Approval: transport.NewApprovalHandler(approvalUseCase, log),
		Manual:   transport.NewManualRefundHandler(manualUseCase, log),
		Holds: transport.NewSettlementHoldHandler(&usecase.SettlementHoldUseCase{Store: repository.SettlementHoldRepository{Pool: dbPool},
			Vendors: repository.NewPayoutRepository(dbPool), Tx: tx, Log: log}, log),
		AdminGuard: adminGuard,
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
	if cfg.VendorActionNotices {
		if bus.Publish {
			go vendorNotices.Run(syncCtx, func(ctx context.Context, n repository.VendorNotice) error {
				env, err := events.VendorPayoutActionEvent("payout-notice-"+n.ID, events.VendorPayoutAction{VendorID: n.VendorID, PayoutID: n.PayoutItemID, Outcome: n.Outcome})
				if err != nil {
					return err
				}
				return bus.Bus.Publish(ctx, env.WithCorrelation(""))
			}, log)
		} else {
			// The notices are kept and relayed once the event bus is back.
			log.Warn().Msg("payment_vendor_notices_wait_for_event_bus")
		}
	}
	bus.Run(syncCtx,
		eventbus.Subscription{Durable: "payment-settlements", Types: []string{events.VendorOrderSettleable}, Handle: transport.SettleableHandler(settlementUseCase)},
		eventbus.Subscription{Durable: "payment-rejected-outcomes", Types: []string{events.PaymentOutcomeRejected}, Handle: transport.OutcomeRejectedHandler(orderSync, refundSync)},
	)
	go (usecase.PaymentWorker{Payments: paymentUseCase, Reconciliation: reconUseCase, Log: log}).Run(syncCtx)
	go manualUseCase.Run(syncCtx)
	adminGroup := router.Group("/api/payments/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin"), adminGuard)
	slaRoles := identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}
	slaStore := casesla.Store{Pool: dbPool}
	casesla.Register(adminGroup, casesla.Service{Repo: slaStore, Roles: slaRoles, Permissions: adminaccess.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}}, log)
	go (casesla.Worker{Store: slaStore, Owner: "payment", Config: slaConfig, Roles: slaRoles, Permissions: adminaccess.Client{URL: internalServices.IdentityURL, Key: internalServices.Key}, Publisher: bus.Bus, Log: log}).Run(syncCtx)
	bus.RegisterAdmin(adminGroup, roles)
	adminaudit.Register(adminGroup, "/audit-events",
		adminaudit.Source{Name: "payment", SQL: repository.AuditSearchSQL + " UNION ALL " + eventbus.InboxAuditSearchSQL + " UNION ALL " + casesla.AuditSearchSQL, DB: dbPool, Roles: roles}, log)

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
