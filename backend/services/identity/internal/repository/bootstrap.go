package repository

import (
	"context"
	"fmt"

	"shopee/backend/services/identity/internal/domain"
)

func (r *UserRepository) CreateFirstAdmin(ctx context.Context, user *domain.User, operator string) error {
	db := connection(ctx, r.pool)
	if _, err := db.Exec(ctx, `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	var exists bool
	if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE role='admin')`).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("an admin already exists; bootstrap refused")
	}
	if err := r.Create(ctx, user); err != nil {
		return err
	}
	return r.Audit(ctx, "", user.ID, "admin_bootstrap", operator)
}
