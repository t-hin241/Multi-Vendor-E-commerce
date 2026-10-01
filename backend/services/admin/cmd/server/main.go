// Command server runs the admin service: the read-only operations
// dashboard and audit search over the domain services' admin APIs.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"shopee/backend/pkg/authjwt"
	sessionconfig "shopee/backend/pkg/config"
	"shopee/backend/pkg/health"
	"shopee/backend/pkg/identityclient"
	"shopee/backend/pkg/logger"
	"shopee/backend/pkg/platform/natsclient"
	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/pkg/platform/redisclient"
	"shopee/backend/pkg/shutdown"

	"shopee/backend/services/admin/internal/adapter"
	"shopee/backend/services/admin/internal/config"
	"shopee/backend/services/admin/internal/transport"
	"shopee/backend/services/admin/internal/usecase"
)

const serviceName = "admin"

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, serviceName+": config error:", err)
		os.Exit(1)
	}
	internalServices, err := sessionconfig.LoadInternalServices()
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
		log.Fatal().Err(err).Msg("session verifier configuration failed")
	}
	jwtManager.SetVerifier(verifier)

	upstreams := make([]usecase.Upstream, 0, len(cfg.Upstreams))
	for _, u := range cfg.Upstreams {
		upstreams = append(upstreams, usecase.Upstream{Name: u.Name, URL: u.URL})
	}
	svc := usecase.Service{
		Upstreams: upstreams,
		Reader:    adapter.Client{HTTP: &http.Client{Timeout: cfg.UpstreamTimeout + time.Second}},
		Roles:     identityclient.Client{URL: internalServices.IdentityURL, Key: internalServices.Key},
		Timeout:   cfg.UpstreamTimeout,
		Log:       log,
	}

	router := transport.NewRouter(cfg.Base.Env, log, jwtManager, svc,
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
		Addr:              ":" + cfg.Base.Port,
		Handler:           router,
		ReadHeaderTimeout: cfg.Base.HTTPReadTimeout,
		ReadTimeout:       cfg.Base.HTTPReadTimeout,
		WriteTimeout:      30 * time.Second,
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
