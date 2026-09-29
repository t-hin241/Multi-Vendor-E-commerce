package usecase

import (
	"context"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"shopee/backend/services/identity/internal/domain"
)

type BootstrapStore interface {
	CreateFirstAdmin(context.Context, *domain.User, string) error
}

func BootstrapAdmin(ctx context.Context, tx Transactions, store BootstrapStore, email, password, name, operator string) error {
	email = domain.NormalizeEmail(email)
	if err := domain.ValidateRegistration(email, password, name, domain.RoleBuyer); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return err
	}
	return tx.Run(ctx, func(ctx context.Context) error {
		return store.CreateFirstAdmin(ctx, &domain.User{Email: email, PasswordHash: string(hash), FullName: strings.TrimSpace(name), Role: domain.RoleAdmin}, operator)
	})
}
