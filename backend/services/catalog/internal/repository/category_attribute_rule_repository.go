package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/catalog/internal/domain"
)

type CategoryAttributeRuleRepository struct {
	pool *pgxpool.Pool
}

func NewCategoryAttributeRuleRepository(pool *pgxpool.Pool) *CategoryAttributeRuleRepository {
	return &CategoryAttributeRuleRepository{pool: pool}
}

// CurrentVersion returns the highest version already recorded for this
// (category, attribute) pair, or 0 if none exists yet — the caller inserts
// version+1 next. Mirrors commission_rules' insert-only "current = latest"
// convention, scoped per pair instead of globally.
func (r *CategoryAttributeRuleRepository) CurrentVersion(ctx context.Context, categoryID, attributeID string) (int, error) {
	const query = `SELECT COALESCE(MAX(version), 0) FROM category_attribute_rules WHERE category_id = $1 AND attribute_id = $2`
	var version int
	err := r.pool.QueryRow(ctx, query, categoryID, attributeID).Scan(&version)
	return version, err
}

func (r *CategoryAttributeRuleRepository) Insert(ctx context.Context, rule *domain.CategoryAttributeRule) error {
	const query = `
		INSERT INTO category_attribute_rules (category_id, attribute_id, version, is_required, is_excluded, position, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`
	return r.pool.QueryRow(ctx, query,
		rule.CategoryID, rule.AttributeID, rule.Version, rule.IsRequired, rule.IsExcluded, rule.Position, rule.CreatedBy,
	).Scan(&rule.ID, &rule.CreatedAt)
}

// CurrentRulesForCategories batch-fetches the current (highest-version) rule
// per (category_id, attribute_id) pair across several categories at once —
// used to resolve a leaf category's whole ancestor chain in one round trip.
func (r *CategoryAttributeRuleRepository) CurrentRulesForCategories(ctx context.Context, categoryIDs []string) ([]*domain.CategoryAttributeRule, error) {
	if len(categoryIDs) == 0 {
		return nil, nil
	}
	const query = `
		SELECT DISTINCT ON (category_id, attribute_id)
			id, category_id, attribute_id, version, is_required, is_excluded, position, created_by, created_at
		FROM category_attribute_rules
		WHERE category_id = ANY($1)
		ORDER BY category_id, attribute_id, version DESC`
	rows, err := r.pool.Query(ctx, query, categoryIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []*domain.CategoryAttributeRule
	for rows.Next() {
		var rule domain.CategoryAttributeRule
		if err := rows.Scan(&rule.ID, &rule.CategoryID, &rule.AttributeID, &rule.Version, &rule.IsRequired, &rule.IsExcluded, &rule.Position, &rule.CreatedBy, &rule.CreatedAt); err != nil {
			return nil, err
		}
		rules = append(rules, &rule)
	}
	return rules, rows.Err()
}
