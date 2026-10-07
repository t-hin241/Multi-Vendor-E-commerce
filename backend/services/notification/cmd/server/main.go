// Command server runs the notification service: notifications recorded in
// PostgreSQL and delivered by Asynq workers (Redis), the recovery loop that
// requeues lost jobs from PostgreSQL, password reset delivery, and the
// admin delivery view.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"shopee/backend/pkg/adminaudit"
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
	"shopee/backend/services/notification/internal/adapter"
	"shopee/backend/services/notification/internal/config"
	"shopee/backend/services/notification/internal/repository"
	"shopee/backend/services/notification/internal/sender"
	"shopee/backend/services/notification/internal/sender/mock"
	smtpsender "shopee/backend/services/notification/internal/sender/smtp"
	"shopee/backend/services/notification/internal/taskqueue"
	"shopee/backend/services/notification/internal/transport"
	"shopee/backend/services/notification/internal/usecase"
)

const serviceName = "notification"

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
	identityClient := adapter.NewHTTPIdentityClient(cfg.IdentityServiceURL, cfg.IdentityServiceKey)
	roles := identityclient.Client{URL: cfg.IdentityServiceURL, Key: cfg.IdentityServiceKey}
	relay := smtpsender.Relay{Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.SMTPFrom, AllowPlaintext: cfg.SMTPAllowPlaintext}
	var emailSender sender.Sender = smtpsender.Sender{Relay: relay}
	if cfg.EmailProvider == "mock" {
		emailSender = mock.New(log)
		log.Warn().Msg("notification_mock_sender_in_use")
	}

	const sendTimeout = 20 * time.Second
	notificationRepo := repository.NewNotificationRepository(dbPool)
	jobs := taskqueue.New(redisClient, taskqueue.QueueName, sendTimeout+40*time.Second)
	if err := prometheus.Register(jobs.Collector()); err != nil {
		log.Fatal().Err(err).Msg("queue metrics failed")
	}
	notificationUseCase := usecase.NewNotificationUseCase(usecase.Deps{
		Store: notificationRepo, Tx: repository.Transactions{Pool: dbPool}, Identity: identityClient,
		Sender: emailSender, Roles: roles, Log: log, SendTimeout: sendTimeout, Queue: jobs,
	})
	stopJobs := func() {}
	if !cfg.DeliveryPaused {
		stopJobs, err = jobs.Start(notificationUseCase.Deliver, cfg.WorkerConcurrency, cfg.Base.ShutdownTimeout, log)
		if err != nil {
			log.Fatal().Err(err).Msg("notification_workers_start_failed")
		}
	}
	maintenance := &usecase.Maintenance{UseCase: notificationUseCase, AttemptRetention: cfg.AttemptRetention, Paused: cfg.DeliveryPaused}
	maintenanceCtx, stopMaintenance := context.WithCancel(ctx)
	defer stopMaintenance()
	go maintenance.Run(maintenanceCtx)
	// PLT-03: notification requests arrive from the event bus (the internal
	// HTTP route stays for producers in rollback mode).
	bus.Run(maintenanceCtx, eventbus.Subscription{Durable: "notification-requests",
		Types: []string{events.OrderNotificationRequested, events.VendorNotificationRequested, events.OrderWorkItemReminder, events.OrderWorkItemOverdue, events.PaymentWorkItemReminder, events.PaymentWorkItemOverdue, events.ShipmentWorkItemReminder, events.ShipmentWorkItemOverdue}, Handle: transport.NotificationRequestedHandler(notificationUseCase)})

	internalHandler := transport.NewInternalHandler(notificationUseCase, log)
	adminHandler := transport.NewAdminHandler(notificationUseCase, log)

	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, cfg.InternalVerifier, internalHandler, adminHandler,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return dbPool.Ping(ctx) }},
		health.Checker{Name: "redis", Ping: func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }},
	)
	adminGroup := router.Group("/api/notifications/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin"))
	bus.RegisterAdmin(adminGroup, roles)
	adminaudit.Register(adminGroup, "/audit-events",
		adminaudit.Source{Name: "notification", SQL: repository.AuditSearchSQL + " UNION ALL " + eventbus.InboxAuditSearchSQL, DB: dbPool, Roles: roles}, log)

	resetUseCase := &usecase.PasswordResetUseCase{Source: adapter.ResetSource{URL: cfg.IdentityServiceURL, Key: cfg.ResetDeliveryKey}, Sender: smtpsender.ResetSender{Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.SMTPFrom, AllowPlaintext: cfg.SMTPAllowPlaintext}}
	transport.RegisterPasswordReset(router, cfg.ResetDeliveryKey, resetUseCase, log)
	srv := &http.Server{
		Addr:              ":" + cfg.Base.Port,
		Handler:           router,
		ReadHeaderTimeout: cfg.Base.HTTPReadTimeout,
		ReadTimeout:       cfg.Base.HTTPReadTimeout,
		WriteTimeout:      cfg.Base.HTTPWriteTimeout,
		IdleTimeout:       cfg.Base.HTTPIdleTimeout,
	}

	go func() {
		log.Info().Str("port", cfg.Base.Port).Str("email_provider", cfg.EmailProvider).Bool("delivery_paused", cfg.DeliveryPaused).Msg(serviceName + "_starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg(serviceName + "_listen_failed")
		}
	}()

	shutdown.WaitForSignal(log, srv, cfg.Base.ShutdownTimeout, func() {
		stopMaintenance()
		bus.Close(cfg.Base.ShutdownTimeout)
		stopJobs()
	})
}
