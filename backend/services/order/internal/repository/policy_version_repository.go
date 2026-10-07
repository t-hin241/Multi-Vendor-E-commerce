package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/order/internal/domain"
)

// PolicyVersionRepository is Order's read model of published policy
// versions and the policy snapshots of orders.
type PolicyVersionRepository struct{ pool *pgxpool.Pool }

func NewPolicyVersionRepository(pool *pgxpool.Pool) *PolicyVersionRepository {
	return &PolicyVersionRepository{pool: pool}
}

// nullableJSON encodes a snapshot, or NULL for none.
func nullableJSON(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && rv.IsNil() {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode policy snapshot: %w", err)
	}
	return b, nil
}

// Insert stores a published version once; a replay of the same version is
// a no-op (versions are immutable).
func (r *PolicyVersionRepository) Insert(ctx context.Context, v domain.PolicyVersion) (bool, error) {
	refs, err := json.Marshal(v.RuleRefs)
	if err != nil {
		return false, err
	}
	var vendorID *string
	if v.Scope == "shop" {
		vendorID = &v.VendorID
	}
	tag, err := connection(ctx, r.pool).Exec(ctx, `
		INSERT INTO policy_versions (policy_id, scope, vendor_id, kind, version, content_hash, rule_refs, effective_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (policy_id) DO NOTHING`,
		v.PolicyID, v.Scope, vendorID, v.Kind, v.Version, v.ContentHash, refs, v.EffectiveAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func scanPolicyVersions(rows pgx.Rows, err error) ([]domain.PolicyVersion, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.PolicyVersion{}
	for rows.Next() {
		var v domain.PolicyVersion
		var vendorID *string
		var refs []byte
		if err := rows.Scan(&v.PolicyID, &v.Scope, &vendorID, &v.Kind, &v.Version, &v.ContentHash, &refs, &v.EffectiveAt); err != nil {
			return nil, err
		}
		if vendorID != nil {
			v.VendorID = *vendorID
		}
		if err := json.Unmarshal(refs, &v.RuleRefs); err != nil {
			return nil, fmt.Errorf("decode rule refs: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

const policyVersionColumns = `policy_id, scope, vendor_id, kind, version, content_hash, rule_refs, effective_at`

// Find returns one version, or nil.
func (r *PolicyVersionRepository) Find(ctx context.Context, policyID string) (*domain.PolicyVersion, error) {
	items, err := scanPolicyVersions(connection(ctx, r.pool).Query(ctx, `SELECT `+policyVersionColumns+` FROM policy_versions WHERE policy_id = $1`, policyID))
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0], nil
}

// MarketplaceAt returns, per kind, the latest marketplace version in force
// at t (rows only, ActiveAt decides).
func (r *PolicyVersionRepository) MarketplaceAt(ctx context.Context, t time.Time) ([]domain.PolicyVersion, error) {
	return scanPolicyVersions(connection(ctx, r.pool).Query(ctx, `SELECT DISTINCT ON (kind) `+policyVersionColumns+`
		FROM policy_versions WHERE scope = 'marketplace' AND effective_at <= $1 ORDER BY kind, effective_at DESC`, t))
}

// ShopsAt returns each shop's policy in force at t.
func (r *PolicyVersionRepository) ShopsAt(ctx context.Context, vendorIDs []string, t time.Time) (map[string]domain.PolicyVersion, error) {
	items, err := scanPolicyVersions(connection(ctx, r.pool).Query(ctx, `SELECT DISTINCT ON (vendor_id) `+policyVersionColumns+`
		FROM policy_versions WHERE scope = 'shop' AND vendor_id::text = ANY($1) AND effective_at <= $2 ORDER BY vendor_id, effective_at DESC`, vendorIDs, t))
	if err != nil {
		return nil, err
	}
	out := map[string]domain.PolicyVersion{}
	for _, v := range items {
		out[v.VendorID] = v
	}
	return out, nil
}

// OrderSnapshot returns an order's snapshot and its vendor orders'
// (nil for an order placed before snapshots existed).
func (r *PolicyVersionRepository) OrderSnapshot(ctx context.Context, orderID string) (*domain.OrderPolicySnapshot, map[string]*domain.VendorPolicySnapshot, error) {
	var raw []byte
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT policy_snapshot FROM orders WHERE id = $1`, orderID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	var order *domain.OrderPolicySnapshot
	if raw != nil {
		order = &domain.OrderPolicySnapshot{}
		if err := json.Unmarshal(raw, order); err != nil {
			return nil, nil, fmt.Errorf("decode order policy snapshot: %w", err)
		}
	}
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT id, policy_snapshot FROM vendor_orders WHERE order_id = $1 AND policy_snapshot IS NOT NULL`, orderID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	vendors := map[string]*domain.VendorPolicySnapshot{}
	for rows.Next() {
		var id string
		var b []byte
		if err := rows.Scan(&id, &b); err != nil {
			return nil, nil, err
		}
		var v domain.VendorPolicySnapshot
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, nil, fmt.Errorf("decode vendor policy snapshot: %w", err)
		}
		vendors[id] = &v
	}
	return order, vendors, rows.Err()
}

// VendorSnapshot returns one vendor order's snapshot, or nil (legacy).
func (r *PolicyVersionRepository) VendorSnapshot(ctx context.Context, vendorOrderID string) (*domain.VendorPolicySnapshot, error) {
	var raw []byte
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT policy_snapshot FROM vendor_orders WHERE id = $1`, vendorOrderID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOrderNotFound
	}
	if err != nil || raw == nil {
		return nil, err
	}
	var v domain.VendorPolicySnapshot
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("decode vendor policy snapshot: %w", err)
	}
	return &v, nil
}
