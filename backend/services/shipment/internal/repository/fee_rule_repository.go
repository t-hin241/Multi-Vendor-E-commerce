package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/shipment/internal/domain"
)

var ErrFeeRuleNotFound = errors.New("repository: fee rule not found")

const feeRuleColumns = `id, carrier_id, zone_id, version, base_fee_amount, free_weight_grams, extra_fee_per_kg, created_by, created_at`

type FeeRuleRepository struct {
	pool *pgxpool.Pool
}

func NewFeeRuleRepository(pool *pgxpool.Pool) *FeeRuleRepository {
	return &FeeRuleRepository{pool: pool}
}

func scanFeeRule(row pgx.Row) (*domain.FeeRule, error) {
	var f domain.FeeRule
	err := row.Scan(&f.ID, &f.CarrierID, &f.ZoneID, &f.Version, &f.BaseFeeAmount, &f.FreeWeightGrams, &f.ExtraFeePerKg, &f.CreatedBy, &f.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFeeRuleNotFound
		}
		return nil, err
	}
	return &f, nil
}

// CurrentVersion returns the highest existing version for a (carrier,
// zone) pair, 0 if none exists yet — the caller inserts version+1, same
// convention as catalog's category_attribute_rule_repository.
func (r *FeeRuleRepository) CurrentVersion(ctx context.Context, carrierID, zoneID string) (int, error) {
	const query = `SELECT COALESCE(MAX(version), 0) FROM shipping_fee_rules WHERE carrier_id = $1 AND zone_id = $2`
	var version int
	if err := r.pool.QueryRow(ctx, query, carrierID, zoneID).Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

func (r *FeeRuleRepository) Insert(ctx context.Context, f *domain.FeeRule) error {
	const query = `
		INSERT INTO shipping_fee_rules (carrier_id, zone_id, version, base_fee_amount, free_weight_grams, extra_fee_per_kg, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`
	return r.pool.QueryRow(ctx, query, f.CarrierID, f.ZoneID, f.Version, f.BaseFeeAmount, f.FreeWeightGrams, f.ExtraFeePerKg, f.CreatedBy).
		Scan(&f.ID, &f.CreatedAt)
}

// FindCurrent returns the highest-version fee rule for a (carrier, zone)
// pair — the rule actually applied when quoting a new shipment.
func (r *FeeRuleRepository) FindCurrent(ctx context.Context, carrierID, zoneID string) (*domain.FeeRule, error) {
	const query = `
		SELECT ` + feeRuleColumns + `
		FROM shipping_fee_rules
		WHERE carrier_id = $1 AND zone_id = $2
		ORDER BY version DESC
		LIMIT 1`
	row := r.pool.QueryRow(ctx, query, carrierID, zoneID)
	return scanFeeRule(row)
}

func (r *FeeRuleRepository) List(ctx context.Context) ([]*domain.FeeRule, error) {
	const query = `SELECT DISTINCT ON (carrier_id, zone_id) ` + feeRuleColumns + `
		FROM shipping_fee_rules ORDER BY carrier_id, zone_id, version DESC`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.FeeRule
	for rows.Next() {
		f, err := scanFeeRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
