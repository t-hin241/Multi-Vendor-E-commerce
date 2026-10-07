// Command bootstrap-access grants access.manage to one existing active
// admin when no access manager exists yet (AF-19). It never creates an
// account and refuses once anyone holds access.manage; the grant is
// audited with the operator and reason.
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
	cfg, err := config.LoadAccessBootstrap()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Access bootstrap configuration invalid")
		os.Exit(1)
	}
	pool, err := postgres.NewPool(context.Background(), cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Access bootstrap database unavailable")
		os.Exit(1)
	}
	defer pool.Close()
	uc := &usecase.AccessUseCase{Store: repository.AccessRepository{Pool: pool}, Users: repository.NewUserRepository(pool),
		Tx: repository.Transactions{Pool: pool}}
	if err = uc.BootstrapAccessManager(context.Background(), cfg.Email, cfg.Operator, cfg.Reason); err != nil {
		fmt.Fprintln(os.Stderr, "Access bootstrap refused or failed; an access manager may already exist or the account is not an active admin")
		os.Exit(1)
	}
	fmt.Println("access.manage granted; audit recorded. Grant the other bundles from the admin access screen.")
}
