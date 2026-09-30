package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/order/internal/domain"
)

var ErrCommissionRuleNotFound = errors.New("repository: commission rule not found")

type CommissionRuleRepository struct {
	pool *pgxpool.Pool
}

func NewCommissionRuleRepository(pool *pgxpool.Pool) *CommissionRuleRepository {
	return &CommissionRuleRepository{pool: pool}
}

// Create inserts the next rule version; the version comes from a sequence,
// so concurrent admins never share one.
func (r *CommissionRuleRepository) Create(ctx context.Context, rule *domain.CommissionRule) error {
	return connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO commission_rules (rate_bps, created_by) VALUES ($1, $2)
		RETURNING id, version, created_at`, rule.RateBps, rule.CreatedBy).Scan(&rule.ID, &rule.Version, &rule.CreatedAt)
}

// FindCurrent returns the highest rule version — the one checkout
// snapshots right now.
func (r *CommissionRuleRepository) FindCurrent(ctx context.Context) (*domain.CommissionRule, error) {
	var rule domain.CommissionRule
	err := connection(ctx, r.pool).QueryRow(ctx,
		`SELECT id, version, rate_bps, created_by, created_at FROM commission_rules ORDER BY version DESC LIMIT 1`,
	).Scan(&rule.ID, &rule.Version, &rule.RateBps, &rule.CreatedBy, &rule.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCommissionRuleNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

func (r *CommissionRuleRepository) List(ctx context.Context, limit, offset int) ([]*domain.CommissionRule, error) {
	rows, err := connection(ctx, r.pool).Query(ctx,
		`SELECT id, version, rate_bps, created_by, created_at FROM commission_rules ORDER BY version DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*domain.CommissionRule{}
	for rows.Next() {
		var rule domain.CommissionRule
		if err := rows.Scan(&rule.ID, &rule.Version, &rule.RateBps, &rule.CreatedBy, &rule.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &rule)
	}
	return out, rows.Err()
}
