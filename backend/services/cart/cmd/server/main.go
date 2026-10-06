// Command server runs the cart service.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	sessionconfig "shopee/backend/pkg/config"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/logger"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/shutdown"
	"shopee/backend/pkg/telemetry"

	"shopee/backend/services/cart/internal/adapter"
	"shopee/backend/services/cart/internal/config"
	"shopee/backend/services/cart/internal/repository"
	"shopee/backend/services/cart/internal/transport"
	"shopee/backend/services/cart/internal/usecase"
)

const serviceName = "cart"

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
	// Plan 14: the runtime role reads and writes rows of this database only.
	if problems, err := postgres.CheckRuntimeRole(ctx, dbPool, cfg.Base.Env == "production"); err != nil {
		log.Fatal().Err(err).Msg("database role check failed")
	} else if len(problems) > 0 {
		log.Warn().Strs("problems", problems).Msg("database_role_too_powerful")
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
	catalogClient := adapter.NewHTTPCatalogClient(cfg.CatalogServiceURL, internalServices.Key)
	inventoryClient := adapter.NewHTTPInventoryClient(cfg.InventoryServiceURL, internalServices.Key)

	cartRepo := repository.NewCartRepository(dbPool)
	cartItemRepo := repository.NewCartItemRepository(dbPool)
	operationRepo := repository.NewCheckoutOperationRepository(dbPool)
	cartUseCase := usecase.NewCartUseCase(repository.Transactions{Pool: dbPool}, cartRepo, cartItemRepo, operationRepo, catalogClient, inventoryClient, log)
	cartHandler := transport.NewCartHandler(cartUseCase, log)
	internalHandler := transport.NewInternalHandler(cartUseCase, log)

	retentionCtx, stopRetention := context.WithCancel(ctx)
	defer stopRetention()
	retention := usecase.NewRetentionWorker(repository.NewRetentionRepository(dbPool), usecase.RetentionPolicy{
		Enabled: cfg.Retention.Enabled, CartIdle: cfg.Retention.CartIdle,
		OperationTTL: cfg.Retention.OperationTTL, Interval: cfg.Retention.Interval,
	}, log)
	go retention.Run(retentionCtx)

	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, cartHandler, internalHandler, internalServices.Verifier,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return dbPool.Ping(ctx) }},
	)

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

	shutdown.WaitForSignal(log, srv, cfg.Base.ShutdownTimeout, nil)
}
