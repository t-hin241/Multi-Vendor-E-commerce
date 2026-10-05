// Command server runs the Shopee API Gateway: the single public entry point
// that terminates client requests and reverse-proxies them to the backend
// service that owns each API prefix.
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"shopee/backend/gateway/internal/config"
	"shopee/backend/gateway/internal/transport"
	platformconfig "shopee/backend/pkg/config"
	"shopee/backend/pkg/logger"
	"shopee/backend/pkg/shutdown"
	"shopee/backend/pkg/telemetry"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gateway: config error:", err)
		os.Exit(1)
	}

	log := logger.New("gateway", cfg.Env, cfg.LogLevel)

	telemetryCfg, err := platformconfig.LoadTelemetry()
	if err != nil {
		log.Fatal().Err(err).Msg("telemetry configuration invalid")
	}
	tel, err := telemetry.Setup("gateway", telemetry.Options(telemetryCfg), log)
	if err != nil {
		log.Fatal().Err(err).Msg("telemetry start failed")
	}
	defer tel.Close()

	router, err := transport.NewRouter(cfg, log)
	if err != nil {
		log.Fatal().Err(err).Msg("gateway: router setup failed")
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    transport.MaxHeaderBytes,
	}

	go func() {
		log.Info().Str("port", cfg.Port).Msg("gateway_starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("gateway_listen_failed")
		}
	}()

	shutdown.WaitForSignal(log, srv, 10*time.Second, nil)
}
