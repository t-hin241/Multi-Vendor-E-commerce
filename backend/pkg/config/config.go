// Package config loads process configuration from environment variables.
// Every service reads config exclusively from the environment; no secret
// value is ever hard-coded here.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Base holds the configuration fields every backend service needs.
type Base struct {
	ServiceName string
	Env         string
	Port        string
	LogLevel    string
	DatabaseURL string
	RedisURL    string
	NATSURL     string
	// EventPublishing is how producers deliver domain events: "jetstream"
	// (event bus, default) or "http" (the previous direct calls; rollback
	// only). Consumers always read the event bus.
	EventPublishing string

	ShutdownTimeout  time.Duration
	HTTPReadTimeout  time.Duration
	HTTPWriteTimeout time.Duration
	HTTPIdleTimeout  time.Duration
}

// LoadBase reads the common configuration shared by all services. It fails
// fast (returns an error) when a required variable is missing so a
// misconfigured service never starts silently.
func LoadBase(serviceName, defaultPort string) (Base, error) {
	databaseURL, err := requireEnv("DATABASE_URL")
	if err != nil {
		return Base{}, err
	}
	if databaseURL, err = withPoolBudget(databaseURL); err != nil {
		return Base{}, err
	}
	env := getEnv("ENV", "development")
	if env == "production" {
		if u, perr := url.Parse(databaseURL); perr == nil && u.User != nil {
			if pw, set := u.User.Password(); set && (pw == "CHANGE_ME" || len(pw) < 16) {
				return Base{}, fmt.Errorf("config: production requires a generated database password (deploy/gen-db-passwords.sh)")
			}
		}
	}

	publishing := getEnv("EVENT_PUBLISHING", "jetstream")
	if publishing != "jetstream" && publishing != "http" {
		return Base{}, fmt.Errorf("config: EVENT_PUBLISHING must be jetstream or http")
	}

	return Base{
		EventPublishing:  publishing,
		ServiceName:      serviceName,
		Env:              env,
		Port:             getEnv("PORT", defaultPort),
		LogLevel:         getEnv("LOG_LEVEL", "info"),
		DatabaseURL:      databaseURL,
		RedisURL:         getEnv("REDIS_URL", "redis://localhost:6379/0"),
		NATSURL:          getEnv("NATS_URL", "nats://localhost:4222"),
		ShutdownTimeout:  getEnvDuration("SHUTDOWN_TIMEOUT", 10*time.Second),
		HTTPReadTimeout:  getEnvDuration("HTTP_READ_TIMEOUT", 5*time.Second),
		HTTPWriteTimeout: getEnvDuration("HTTP_WRITE_TIMEOUT", 60*time.Second),
		HTTPIdleTimeout:  getEnvDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
	}, nil
}

// RequireEventBusPassword reads this service's event-bus password
// (EVENTBUS_PASSWORD; the broker identifies the service by its name and
// lets it publish only its own event types). Only services on the event
// bus call this. Compose gives the broker and the service the same value,
// so it is used exactly as set; production refuses a weak or placeholder
// value.
func RequireEventBusPassword() (string, error) {
	password, err := requireEnv("EVENTBUS_PASSWORD")
	if err != nil {
		return "", err
	}
	// The broker receives it inside a quoted string (docker-compose.yml).
	if strings.ContainsAny(password, `"\`) {
		return "", fmt.Errorf("config: EVENTBUS_PASSWORD (the service key) must not contain quotes or backslashes")
	}
	if getEnv("ENV", "development") == "production" && (len(password) < 32 || password == "CHANGE_ME") {
		return "", fmt.Errorf("config: EVENTBUS_PASSWORD must contain at least 32 characters in production")
	}
	return password, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func requireEnv(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", fmt.Errorf("config: required environment variable %q is not set", key)
	}
	return v, nil
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

// withPoolBudget adds this service's share of the database to a
// postgres:// URL unless the URL sets it already (PLT-04): at most
// DB_MAX_CONNS connections (default 8: 11 services stay under PostgreSQL's
// default 100) and DB_STATEMENT_TIMEOUT seconds per statement (default 30),
// so one slow query cannot hold a connection forever.
func withPoolBudget(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return raw, nil // keyword/value form: left as configured
	}
	q := u.Query()
	if q.Get("pool_max_conns") == "" {
		n := getEnv("DB_MAX_CONNS", "8")
		if v, err := strconv.Atoi(n); err != nil || v < 1 || v > 100 {
			return "", fmt.Errorf("config: DB_MAX_CONNS must be between 1 and 100")
		}
		q.Set("pool_max_conns", n)
	}
	if q.Get("statement_timeout") == "" {
		secs, err := strconv.Atoi(getEnv("DB_STATEMENT_TIMEOUT", "30"))
		if err != nil || secs < 1 || secs > 600 {
			return "", fmt.Errorf("config: DB_STATEMENT_TIMEOUT must be between 1 and 600 seconds")
		}
		q.Set("statement_timeout", strconv.Itoa(secs*1000))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
