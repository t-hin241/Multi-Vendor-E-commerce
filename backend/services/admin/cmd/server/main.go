// Command server runs the admin service.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"shopee/backend/pkg/config"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/logger"
	"shopee/backend/pkg/platform/natsclient"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/platform/redisclient"
	"shopee/backend/pkg/shutdown"

	"shopee/backend/services/admin/internal/transport"
)

const serviceName = "admin"
const defaultPort = "8089"

func main() {
	cfg, err := config.LoadBase(serviceName, defaultPort)
	if err != nil {
		fmt.Fprintln(os.Stderr, serviceName+": config error:", err)
		os.Exit(1)
	}

	log := logger.New(serviceName, cfg.Env, cfg.LogLevel)
	ctx := context.Background()

	dbPool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("database connection failed")
	}
	defer dbPool.Close()

	redisClient, err := redisclient.NewClient(ctx, cfg.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("redis connection failed")
	}
	defer redisClient.Close()

	natsConn, err := natsclient.Connect(cfg.NATSURL)
	if err != nil {
		log.Fatal().Err(err).Msg("nats connection failed")
	}
	defer natsConn.Close()

	router := transport.NewRouter(cfg.Env, log,
		health.Checker{Name: "postgres", Ping: func(ctx context.Context) error { return dbPool.Ping(ctx) }},
		health.Checker{Name: "redis", Ping: func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }},
		health.Checker{Name: "nats", Ping: func(ctx context.Context) error {
			if !natsConn.IsConnected() {
				return fmt.Errorf("nats: not connected")
			}
			return nil
		}},
	)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: cfg.HTTPReadTimeout,
		ReadTimeout:       cfg.HTTPReadTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
	}

	go func() {
		log.Info().Str("port", cfg.Port).Msg(serviceName + "_starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg(serviceName + "_listen_failed")
		}
	}()

	shutdown.WaitForSignal(log, srv, cfg.ShutdownTimeout, nil)
}
