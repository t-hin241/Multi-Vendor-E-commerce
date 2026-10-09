// Command server runs the vendor service.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"shopee/backend/pkg/adminaccess"
	"shopee/backend/pkg/adminaudit"
	sessionconfig "shopee/backend/pkg/config"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/identityclient"
	"shopee/backend/pkg/logger"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/platform/objectstorage"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/shutdown"
	"shopee/backend/pkg/telemetry"
	"shopee/backend/services/vendorsvc/internal/adapter"
	"shopee/backend/services/vendorsvc/internal/config"
	"shopee/backend/services/vendorsvc/internal/repository"
	"shopee/backend/services/vendorsvc/internal/transport"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

const serviceName = "vendor"

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
	// PW-008: operator queues past their threshold, for alerting.
	if err := telemetry.RegisterWorkQueues(dbPool, repository.WorkQueues...); err != nil {
		log.Fatal().Err(err).Msg("work queue metrics failed")
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
	bus, err := eventbus.Start(cfg.Base.NATSURL, eventbus.Credentials{Service: serviceName, Password: busPassword}, cfg.Base.EventPublishing, dbPool, log)
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

	objectStore, err := objectstorage.NewClient(ctx, cfg.ObjectStorage)
	if err != nil {
		log.Fatal().Err(err).Msg("object storage connection failed")
	}

	vendorRepo := repository.NewVendorRepository(dbPool)
	auditLogRepo := repository.NewAuditLogRepository(dbPool)
	addressRepo := repository.NewVendorAddressRepository(dbPool)
	outbox := repository.Outbox{Pool: dbPool}
	notices := repository.NotificationOutbox{Pool: dbPool}
	ops := usecase.Operations{Tx: repository.Transactions{Pool: dbPool}, Actors: identityclient.Client{URL: cfg.Internal.IdentityURL, Key: cfg.Internal.Key}, Addresses: addressRepo, Events: outbox, Notices: notices}
	workerCtx, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()
	sendStatus, sendNotice := adapter.BusStatusSender(bus.Bus), adapter.BusNoticeSender(bus.Bus)
	if !bus.Publish {
		sendStatus = adapter.HTTPStatusSender([]string{cfg.CatalogURL, cfg.OrderURL}, cfg.Internal.Key)
		sendNotice = adapter.HTTPNoticeSender(cfg.NotificationServiceURL, cfg.Internal.Key)
	}
	go adapter.DispatchStatus(workerCtx, outbox, sendStatus, log)
	go adapter.DispatchNotices(workerCtx, notices, sendNotice, log)
	vendorUseCase := usecase.NewVendorUseCase(vendorRepo, auditLogRepo, objectStore, log, ops)
	policyRepo := repository.PolicyRepository{Pool: dbPool}
	policyUseCase := &usecase.PolicyUseCase{Policies: policyRepo, Vendors: vendorRepo, Audit: auditLogRepo, Notices: notices,
		Rules: adapter.NewRuleReadinessClient(map[string]string{"order": cfg.OrderURL, "payment": cfg.PaymentURL, "shipment": cfg.ShipmentURL}, cfg.Internal.Key), Ops: ops,
		Enabled: cfg.VersionedPolicies, Log: log}
	sendPolicy := adapter.BusPolicySender(bus.Bus)
	if !bus.Publish {
		sendPolicy = adapter.HTTPPolicySender(cfg.OrderURL, cfg.Internal.Key)
	}
	go adapter.DispatchPolicies(workerCtx, policyRepo, sendPolicy, policyUseCase, log)
	addressUseCase := usecase.NewVendorAddressUseCase(addressRepo, vendorRepo, ops)
	returnDestinations := repository.ReturnDestinationRepository{Pool: dbPool}
	addressUseCase.Destinations = returnDestinations
	// AF-17: shop staff. Authorization reads memberships on every call;
	// invitation emails go out from this worker.
	staffUseCase := &usecase.StaffUseCase{Staff: repository.StaffRepository{Pool: dbPool}, Vendors: vendorRepo,
		Accounts: identityclient.Client{URL: cfg.Internal.IdentityURL, Key: cfg.Internal.Key}, Tx: ops.Tx,
		Mailer: adapter.NewStaffInvitationMailer(cfg.NotificationServiceURL, cfg.Internal.Key), Enabled: cfg.ShopStaff,
		InvitesPaused: cfg.StaffInvitesPaused, FingerprintKey: cfg.StaffFingerprintKey, AcceptURL: cfg.StaffAcceptURL, Log: log}
	go staffUseCase.RunInvitationDelivery(workerCtx)

	vendorHandler := transport.NewVendorHandler(vendorUseCase, log)
	addressHandler := transport.NewVendorAddressHandler(addressUseCase, log)
	adminHandler := transport.NewAdminHandler(vendorUseCase, log)
	internalHandler := transport.NewInternalHandler(vendorUseCase, log)
	policyHandler := transport.NewPolicyHandler(policyUseCase, vendorUseCase, log)
	staffHandler := transport.NewStaffHandler(staffUseCase, log)
	// AF-19: every admin route needs the bundle named in transport.AdminRoutes.
	adminGuard := adminaccess.Guard(adminaccess.Client{URL: cfg.Internal.IdentityURL, Key: cfg.Internal.Key}, transport.AdminRoutes, log)

	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, vendorHandler, addressHandler, adminHandler, internalHandler, policyHandler, staffHandler, adminGuard, cfg.Internal.Verifier,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return dbPool.Ping(ctx) }},
		health.Checker{Name: "object_storage", Ping: objectStore.Ping},
	)

	cipher, err := adapter.NewPayoutCipher(cfg.PayoutKey)
	if err != nil {
		log.Fatal().Msg("payout encryption configuration invalid")
	}
	payoutUC := &usecase.PayoutUseCase{Accounts: repository.PayoutRepository{Pool: dbPool}, Vendors: vendorRepo, Audit: auditLogRepo, Ops: ops, Cipher: cipher,
		Proofs: adminaccess.Client{URL: cfg.Internal.IdentityURL, Key: cfg.Internal.Key}, RequireProof: cfg.AdminReauth}
	(transport.PayoutHandler{UseCase: payoutUC, Log: log, AdminGuard: adminGuard}).Register(router, middleware.RequireAuth(jwtManager), cfg.PayoutServiceKey, cfg.Internal.Verifier)
	// AF-05: where returned goods go; Order reads the verified one.
	(transport.ReturnDestinationHandler{UseCase: &usecase.ReturnDestinationUseCase{Destinations: returnDestinations, Addresses: addressRepo,
		Vendors: vendorRepo, Audit: auditLogRepo, Ops: ops}, Log: log, AdminGuard: adminGuard}).Register(router, middleware.RequireAuth(jwtManager), cfg.Internal.Verifier)

	dashboard := usecase.Dashboard{Vendors: vendorUseCase, Orders: adapter.ReportClient{URL: cfg.OrderURL, Key: cfg.Internal.Key}, Payments: adapter.ReportClient{URL: cfg.PaymentURL, Key: cfg.Internal.Key},
		Access: staffUseCase}
	router.GET("/api/vendor/:vendorId/dashboard", middleware.RequireAuth(jwtManager), middleware.RequireRole("vendor", "buyer"), transport.DashboardHandler(dashboard, log))

	adminaudit.Register(router.Group("/api/vendor/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin"), adminGuard), "/audit-events",
		adminaudit.Source{Name: "vendor", SQL: repository.AuditSearchSQL, DB: dbPool, Roles: identityclient.Client{URL: cfg.Internal.IdentityURL, Key: cfg.Internal.Key}}, log)

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
