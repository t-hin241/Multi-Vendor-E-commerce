// Command server runs the payment service.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"shopee/backend/pkg/authjwt"
	sessionconfig "shopee/backend/pkg/config"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/logger"
	"shopee/backend/pkg/platform/natsclient"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/platform/redisclient"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/shutdown"
	"shopee/backend/pkg/vendorreport"
	"shopee/backend/services/payment/internal/adapter"
	"shopee/backend/services/payment/internal/config"
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

	intentRepo := repository.NewPaymentIntentRepository(dbPool)
	eventRepo := repository.NewPaymentEventRepository(dbPool)

	paymentUseCase := usecase.NewPaymentUseCase(intentRepo, eventRepo, orderClient, paymentProvider, verifier, simulator, cfg.Provider, cfg.PayOSReturnURL, cfg.PayOSCancelURL, log)
	paymentHandler := transport.NewPaymentHandler(paymentUseCase, log)
	webhookHandler := transport.NewWebhookHandler(paymentUseCase, log)

	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, paymentHandler, webhookHandler,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return dbPool.Ping(ctx) }},
		health.Checker{Name: "redis", Ping: func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }},
		health.Checker{Name: "nats", Ping: func(ctx context.Context) error {
			if !natsConn.IsConnected() {
				return fmt.Errorf("nats: not connected")
			}
			return nil
		}},
	)

	router.GET("/internal/vendor-reports/:vendorId", serviceauth.Require(internalServices.Key, serviceauth.Header), vendorreport.Handler(vendorreport.Service{Repository: repository.VendorReport{Pool: dbPool}}, log))

	syncCtx, stopSync := context.WithCancel(ctx)
	defer stopSync()
	go (repository.OrderSync{Pool: dbPool}).Run(syncCtx, func(ctx context.Context, id, outcome string) error {
		if outcome == "captured" {
			return orderClient.MarkPaid(ctx, id)
		}
		return orderClient.MarkPaymentFailed(ctx, id, "Payment failed")
	}, log)
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
