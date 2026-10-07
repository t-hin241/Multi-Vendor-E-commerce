package config

import (
	"fmt"
	"net/url"
	"os"
)

type Bootstrap struct{ DatabaseURL, Email, Password, FullName, Operator string }

func LoadBootstrap() (Bootstrap, error) {
	c := Bootstrap{DatabaseURL: os.Getenv("DATABASE_URL"), Email: os.Getenv("IDENTITY_BOOTSTRAP_EMAIL"), Password: os.Getenv("IDENTITY_BOOTSTRAP_PASSWORD"), FullName: os.Getenv("IDENTITY_BOOTSTRAP_NAME"), Operator: os.Getenv("IDENTITY_BOOTSTRAP_OPERATOR")}
	if os.Getenv("IDENTITY_BOOTSTRAP_CONFIRM") != "true" || c.DatabaseURL == "" || c.Operator == "" {
		return c, fmt.Errorf("explicit bootstrap confirmation, database and operator required")
	}
	return c, nil
}

// AccessBootstrap configures cmd/bootstrap-access (AF-19).
type AccessBootstrap struct{ DatabaseURL, Email, Operator, Reason string }

func LoadAccessBootstrap() (AccessBootstrap, error) {
	c := AccessBootstrap{DatabaseURL: os.Getenv("DATABASE_URL"), Email: os.Getenv("IDENTITY_BOOTSTRAP_ACCESS_EMAIL"),
		Operator: os.Getenv("IDENTITY_BOOTSTRAP_OPERATOR"), Reason: os.Getenv("IDENTITY_BOOTSTRAP_REASON")}
	if os.Getenv("IDENTITY_BOOTSTRAP_CONFIRM") != "true" || c.DatabaseURL == "" || c.Email == "" || c.Operator == "" || c.Reason == "" {
		return c, fmt.Errorf("explicit confirmation, database, admin email, operator and reason required")
	}
	return c, nil
}

// TestDatabaseURL accepts only URLs targeting the identity_test database.
func TestDatabaseURL() (string, error) {
	raw := os.Getenv("IDENTITY_TEST_DATABASE_URL")
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path != "/identity_test" {
		return "", fmt.Errorf("integration tests require a dedicated identity_test database")
	}
	return raw, nil
}
func TestRedisURL() string { return os.Getenv("IDENTITY_TEST_REDIS_URL") }
