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

func (r *CommissionRuleRepository) Create(ctx context.Context, rule *domain.CommissionRule) error {
	const query = `INSERT INTO commission_rules (rate_bps, created_by) VALUES ($1, $2) RETURNING id, created_at`
	return r.pool.QueryRow(ctx, query, rule.RateBps, rule.CreatedBy).Scan(&rule.ID, &rule.CreatedAt)
}

// FindCurrent returns the most recently created rule — the one that
// applies to any vendor order marked paid right now.
func (r *CommissionRuleRepository) FindCurrent(ctx context.Context) (*domain.CommissionRule, error) {
	const query = `SELECT id, rate_bps, created_by, created_at FROM commission_rules ORDER BY created_at DESC LIMIT 1`

	var rule domain.CommissionRule
	err := r.pool.QueryRow(ctx, query).Scan(&rule.ID, &rule.RateBps, &rule.CreatedBy, &rule.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCommissionRuleNotFound
		}
		return nil, err
	}
	return &rule, nil
}

func (r *CommissionRuleRepository) List(ctx context.Context, limit, offset int) ([]*domain.CommissionRule, error) {
	const query = `SELECT id, rate_bps, created_by, created_at FROM commission_rules ORDER BY created_at DESC LIMIT $1 OFFSET $2`

	rows, err := r.pool.Query(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.CommissionRule
	for rows.Next() {
		var rule domain.CommissionRule
		if err := rows.Scan(&rule.ID, &rule.RateBps, &rule.CreatedBy, &rule.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &rule)
	}
	return out, rows.Err()
}
