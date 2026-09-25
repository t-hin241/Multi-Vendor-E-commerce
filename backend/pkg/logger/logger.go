// Package logger provides the structured JSON logger used by every service.
package logger

import (
	"os"

	"github.com/rs/zerolog"
)

// New builds a zerolog.Logger that writes structured JSON to stdout, tagged
// with the owning service name so logs stay attributable once aggregated.
func New(serviceName, env, level string) zerolog.Logger {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	parsedLevel, err := zerolog.ParseLevel(level)
	if err != nil {
		parsedLevel = zerolog.InfoLevel
	}

	return zerolog.New(os.Stdout).
		Level(parsedLevel).
		With().
		Timestamp().
		Str("service", serviceName).
		Str("env", env).
		Logger()
}
