package main

import (
	"context"
	"fmt"
	"os"

	"shopee/backend/pkg/platform/postgres"
	"shopee/backend/services/identity/internal/config"
	"shopee/backend/services/identity/internal/repository"
	"shopee/backend/services/identity/internal/usecase"
)

func main() {
	cfg, err := config.LoadBootstrap()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Bootstrap configuration invalid")
		os.Exit(1)
	}
	pool, err := postgres.NewPool(context.Background(), cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Bootstrap database unavailable")
		os.Exit(1)
	}
	defer pool.Close()
	if err = usecase.BootstrapAdmin(context.Background(), repository.Transactions{Pool: pool}, repository.NewUserRepository(pool), cfg.Email, cfg.Password, cfg.FullName, cfg.Operator); err != nil {
		fmt.Fprintln(os.Stderr, "Bootstrap refused or failed; check configuration and existing admin accounts")
		os.Exit(1)
	}
	fmt.Println("Initial admin created; audit recorded. Remove bootstrap credentials from the process environment.")
}
