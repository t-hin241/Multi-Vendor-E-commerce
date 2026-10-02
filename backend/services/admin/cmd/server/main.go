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
	"shopee/backend/pkg/platform/postgres"
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
	// Plan 14: the runtime role reads and writes rows of this database only.
	if problems, err := postgres.CheckRuntimeRole(ctx, dbPool, cfg.Base.Env == "production"); err != nil {
		log.Fatal().Err(err).Msg("database role check failed")
	} else if len(problems) > 0 {
		log.Warn().Strs("problems", problems).Msg("database_role_too_powerful")
	}

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
