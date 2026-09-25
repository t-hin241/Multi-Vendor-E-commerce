// Package shutdown provides the graceful-shutdown sequence shared by every
// service's HTTP server, so in-flight requests finish before the process
// exits on SIGINT/SIGTERM (Docker Compose stop, orchestrator rollout).
package shutdown

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
)

// WaitForSignal blocks until SIGINT or SIGTERM is received, then shuts the
// HTTP server down within timeout, running cleanup afterwards (closing DB
// pools, Redis and NATS connections).
func WaitForSignal(log zerolog.Logger, srv *http.Server, timeout time.Duration, cleanup func()) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutdown_signal_received")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("http_server_shutdown_error")
	}

	if cleanup != nil {
		cleanup()
	}

	log.Info().Msg("shutdown_complete")
}
