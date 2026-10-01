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

	"shopee/backend/pkg/adminaudit"
	"shopee/backend/pkg/authjwt"
	sessionconfig "shopee/backend/pkg/config"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/identityclient"
	"shopee/backend/pkg/logger"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/platform/natsclient"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/platform/redisclient"
	"shopee/backend/pkg/shutdown"
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

	internalHandler := transport.NewInternalHandler(notificationUseCase, log)
	adminHandler := transport.NewAdminHandler(notificationUseCase, log)

	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, cfg.IdentityServiceKey, internalHandler, adminHandler,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return dbPool.Ping(ctx) }},
		health.Checker{Name: "redis", Ping: func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }},
		health.Checker{Name: "nats", Ping: func(ctx context.Context) error {
			if !natsConn.IsConnected() {
				return fmt.Errorf("nats: not connected")
			}
			return nil
		}},
	)
	adminaudit.Register(router.Group("/api/notifications/admin", middleware.RequireAuth(jwtManager), middleware.RequireRole("admin")), "/audit-events",
		adminaudit.Source{Name: "notification", SQL: repository.AuditSearchSQL, DB: dbPool, Roles: roles}, log)

	resetUseCase := &usecase.PasswordResetUseCase{Source: adapter.ResetSource{URL: cfg.IdentityServiceURL, Key: cfg.ResetDeliveryKey}, Sender: smtpsender.ResetSender{Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.SMTPFrom, AllowPlaintext: cfg.SMTPAllowPlaintext}}
	transport.RegisterPasswordReset(router, cfg.ResetDeliveryKey, resetUseCase, log)
	srv := &http.Server{
		Addr:              ":" + cfg.Base.Port,
		Handler:           router,
		ReadHeaderTimeout: cfg.Base.HTTPReadTimeout,
		ReadTimeout:       cfg.Base.HTTPReadTimeout,
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
		stopJobs()
	})
}
