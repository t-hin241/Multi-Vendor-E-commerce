package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/vendorsvc/internal/domain"
)

var ErrPolicyNotFound = errors.New("repository: policy version not found")

// PolicyRepository stores marketplace and shop policy versions, their
// publication outbox and the marketplace policy audit.
type PolicyRepository struct{ Pool *pgxpool.Pool }

const marketplacePolicyColumns = `id, seq, kind, version, title, summary, content, contact, rule_refs, effective_at, status, content_hash,
	readiness, publication_reason, created_by, published_by, preparing_since, published_at, row_version, created_at, updated_at`

func scanMarketplacePolicy(row pgx.Row) (*domain.MarketplacePolicy, error) {
	var p domain.MarketplacePolicy
	var refs, readiness []byte
	if err := row.Scan(&p.ID, &p.Seq, &p.Kind, &p.Version, &p.Title, &p.Summary, &p.Content, &p.Contact, &refs, &p.EffectiveAt, &p.Status,
		&p.ContentHash, &readiness, &p.PublicationReason, &p.CreatedBy, &p.PublishedBy, &p.PreparingSince, &p.PublishedAt, &p.RowVersion,
		&p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPolicyNotFound
		}
		return nil, err
	}
	p.RuleRefs = map[string]string{}
	if err := json.Unmarshal(refs, &p.RuleRefs); err != nil {
		return nil, fmt.Errorf("decode rule refs: %w", err)
	}
	if len(readiness) > 0 {
		if err := json.Unmarshal(readiness, &p.Readiness); err != nil {
			return nil, fmt.Errorf("decode readiness: %w", err)
		}
	}
	return &p, nil
}

func collectMarketplacePolicies(rows pgx.Rows, err error) ([]*domain.MarketplacePolicy, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.MarketplacePolicy{}
	for rows.Next() {
		p, err := scanMarketplacePolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CreateMarketplace inserts a draft with the next version of its kind. The
// kind is locked for the transaction so two drafts never get one number.
func (r PolicyRepository) CreateMarketplace(ctx context.Context, p *domain.MarketplacePolicy) error {
	q := connection(ctx, r.Pool)
	if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('marketplace_policy:' || $1))`, p.Kind); err != nil {
		return err
	}
	refs, err := json.Marshal(p.RuleRefs)
	if err != nil {
		return err
	}
	return q.QueryRow(ctx, `
		INSERT INTO marketplace_policy_versions (kind, version, title, summary, content, contact, rule_refs, effective_at, content_hash, created_by)
		VALUES ($1, (SELECT COALESCE(MAX(version), 0) + 1 FROM marketplace_policy_versions WHERE kind = $1), $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, seq, version, status, row_version, created_at, updated_at`,
		p.Kind, p.Title, p.Summary, p.Content, p.Contact, refs, p.EffectiveAt, p.ContentHash, p.CreatedBy,
	).Scan(&p.ID, &p.Seq, &p.Version, &p.Status, &p.RowVersion, &p.CreatedAt, &p.UpdatedAt)
}

// FindMarketplace returns a version; inside a transaction it is locked.
func (r PolicyRepository) FindMarketplace(ctx context.Context, id string) (*domain.MarketplacePolicy, error) {
	return scanMarketplacePolicy(connection(ctx, r.Pool).QueryRow(ctx,
		`SELECT `+marketplacePolicyColumns+` FROM marketplace_policy_versions WHERE id = $1`+lockVendor(ctx), id))
}

// ListMarketplace lists versions for admins, newest first.
func (r PolicyRepository) ListMarketplace(ctx context.Context, kind, status string, limit, offset int) ([]*domain.MarketplacePolicy, error) {
	return collectMarketplacePolicies(connection(ctx, r.Pool).Query(ctx, `SELECT `+marketplacePolicyColumns+` FROM marketplace_policy_versions
		WHERE ($1 = '' OR kind = $1) AND ($2 = '' OR status = $2) ORDER BY kind, version DESC LIMIT $3 OFFSET $4`, kind, status, limit, offset))
}

// SaveMarketplaceStatus writes a status step with compare-and-set on the
// row version the caller read; a concurrent change returns ErrStaleVersion.
func (r PolicyRepository) SaveMarketplaceStatus(ctx context.Context, p *domain.MarketplacePolicy) error {
	var readiness []byte
	if p.Readiness != nil {
		var err error
		if readiness, err = json.Marshal(p.Readiness); err != nil {
			return err
		}
	}
	err := connection(ctx, r.Pool).QueryRow(ctx, `
		UPDATE marketplace_policy_versions SET status = $3, readiness = $4, publication_reason = $5, published_by = $6,
		    preparing_since = $7, published_at = $8, row_version = row_version + 1, updated_at = now()
		WHERE id = $1 AND row_version = $2 RETURNING row_version, updated_at`,
		p.ID, p.RowVersion, p.Status, readiness, p.PublicationReason, p.PublishedBy, p.PreparingSince, p.PublishedAt,
	).Scan(&p.RowVersion, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleVersion
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return apperror.Conflict("Another published version of this policy starts at the same time")
	}
	return err
}

// ErrStaleVersion: the row changed since it was read.
var ErrStaleVersion = errors.New("repository: stale policy version")

// ListPreparing returns versions still waiting for their rule owners.
func (r PolicyRepository) ListPreparing(ctx context.Context, limit int) ([]*domain.MarketplacePolicy, error) {
	return collectMarketplacePolicies(connection(ctx, r.Pool).Query(ctx, `SELECT `+marketplacePolicyColumns+` FROM marketplace_policy_versions
		WHERE status = 'preparing' ORDER BY preparing_since, id LIMIT $1`, limit))
}

// ActiveMarketplace returns, per kind, the published version in force at
// now (latest effective_at not after now).
func (r PolicyRepository) ActiveMarketplace(ctx context.Context, now time.Time) ([]*domain.MarketplacePolicy, error) {
	return collectMarketplacePolicies(connection(ctx, r.Pool).Query(ctx, `SELECT DISTINCT ON (kind) `+marketplacePolicyColumns+`
		FROM marketplace_policy_versions WHERE status = 'published' AND effective_at <= $1 ORDER BY kind, effective_at DESC`, now))
}

// PublishedHistory lists a kind's published versions, newest first,
// including scheduled ones.
func (r PolicyRepository) PublishedHistory(ctx context.Context, kind domain.PolicyKind) ([]*domain.MarketplacePolicy, error) {
	return collectMarketplacePolicies(connection(ctx, r.Pool).Query(ctx, `SELECT `+marketplacePolicyColumns+` FROM marketplace_policy_versions
		WHERE status = 'published' AND kind = $1 ORDER BY effective_at DESC`, kind))
}

// PublishedVersion returns one published version, for links kept on orders.
func (r PolicyRepository) PublishedVersion(ctx context.Context, kind domain.PolicyKind, version int) (*domain.MarketplacePolicy, error) {
	return scanMarketplacePolicy(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+marketplacePolicyColumns+` FROM marketplace_policy_versions
		WHERE status = 'published' AND kind = $1 AND version = $2`, kind, version))
}

// RecordPolicyAudit appends an admin decision on a marketplace policy.
func (r PolicyRepository) RecordPolicyAudit(ctx context.Context, policyID, actorID, action string, reason *string) error {
	_, err := connection(ctx, r.Pool).Exec(ctx, `INSERT INTO policy_audit_logs (policy_id, actor_user_id, action, reason, request_id)
		VALUES ($1, $2, $3, $4, $5)`, policyID, actorID, action, reason, middleware.CorrelationID(ctx))
	return err
}

// QueueMarketplaceNotices tells every shop owner about a new marketplace
// policy version, once per version.
func (r PolicyRepository) QueueMarketplaceNotices(ctx context.Context, seq int64) error {
	_, err := connection(ctx, r.Pool).Exec(ctx, `INSERT INTO vendor_notification_outbox (vendor_id, user_id, type, version)
		SELECT id, user_id, 'marketplace_policy_updated', $1 FROM vendors WHERE status IN ('approved', 'suspended')
		ON CONFLICT (vendor_id, version, type) DO NOTHING`, seq)
	return err
}

const shopPolicyColumns = `id, seq, vendor_id, version, content, content_hash, status, source, proposed_by, decided_by, decision_reason, decided_at, created_at`

func scanShopPolicy(row pgx.Row) (*domain.ShopPolicy, error) {
	var p domain.ShopPolicy
	if err := row.Scan(&p.ID, &p.Seq, &p.VendorID, &p.Version, &p.Content, &p.ContentHash, &p.Status, &p.Source, &p.ProposedBy, &p.DecidedBy,
		&p.DecisionReason, &p.DecidedAt, &p.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPolicyNotFound
		}
		return nil, err
	}
	return &p, nil
}

func collectShopPolicies(rows pgx.Rows, err error) ([]*domain.ShopPolicy, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.ShopPolicy{}
	for rows.Next() {
		p, err := scanShopPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CreateShopProposal inserts the shop's next version. One proposal may wait
// for review per shop.
func (r PolicyRepository) CreateShopProposal(ctx context.Context, p *domain.ShopPolicy) error {
	q := connection(ctx, r.Pool)
	if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('shop_policy:' || $1))`, p.VendorID); err != nil {
		return err
	}
	err := q.QueryRow(ctx, `
		INSERT INTO shop_policy_versions (vendor_id, version, content, content_hash, proposed_by)
		VALUES ($1, (SELECT COALESCE(MAX(version), 0) + 1 FROM shop_policy_versions WHERE vendor_id = $1), $2, $3, $4)
		RETURNING id, seq, version, status, source, created_at`,
		p.VendorID, p.Content, p.ContentHash, p.ProposedBy,
	).Scan(&p.ID, &p.Seq, &p.Version, &p.Status, &p.Source, &p.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return apperror.Conflict("This shop already has a policy waiting for review")
	}
	return err
}

// FindShopPolicy returns a shop version; inside a transaction it is locked.
func (r PolicyRepository) FindShopPolicy(ctx context.Context, id string) (*domain.ShopPolicy, error) {
	return scanShopPolicy(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+shopPolicyColumns+` FROM shop_policy_versions WHERE id = $1`+lockVendor(ctx), id))
}

func (r PolicyRepository) ListShopPolicies(ctx context.Context, vendorID string) ([]*domain.ShopPolicy, error) {
	return collectShopPolicies(connection(ctx, r.Pool).Query(ctx, `SELECT `+shopPolicyColumns+` FROM shop_policy_versions
		WHERE vendor_id = $1 ORDER BY version DESC LIMIT 50`, vendorID))
}

// ListShopProposals is the admin review queue, oldest first.
func (r PolicyRepository) ListShopProposals(ctx context.Context, status string, limit, offset int) ([]*domain.ShopPolicy, error) {
	return collectShopPolicies(connection(ctx, r.Pool).Query(ctx, `SELECT `+shopPolicyColumns+` FROM shop_policy_versions
		WHERE ($1 = '' OR status = $1) ORDER BY created_at, id LIMIT $2 OFFSET $3`, status, limit, offset))
}

// DecideShopPolicy records the review of a proposal still waiting.
func (r PolicyRepository) DecideShopPolicy(ctx context.Context, p *domain.ShopPolicy) error {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE shop_policy_versions SET status = $2, decided_by = $3, decision_reason = $4, decided_at = $5
		WHERE id = $1 AND status = 'proposed'`, p.ID, p.Status, p.DecidedBy, p.DecisionReason, p.DecidedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleVersion
	}
	return nil
}

// ApprovedShopPolicy is the shop's public policy: its latest approved one.
func (r PolicyRepository) ApprovedShopPolicy(ctx context.Context, vendorID string) (*domain.ShopPolicy, error) {
	p, err := scanShopPolicy(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+shopPolicyColumns+` FROM shop_policy_versions
		WHERE vendor_id = $1 AND status = 'approved' ORDER BY decided_at DESC, version DESC LIMIT 1`, vendorID))
	if errors.Is(err, ErrPolicyNotFound) {
		return nil, nil
	}
	return p, err
}

// QueuePublication writes vendor.policy_published in the publication's
// transaction (once per version).
func (r PolicyRepository) QueuePublication(ctx context.Context, policyID string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = connection(ctx, r.Pool).Exec(ctx, `INSERT INTO policy_outbox (policy_id, payload) VALUES ($1, $2) ON CONFLICT (policy_id) DO NOTHING`, policyID, b)
	return err
}

// PolicyDelivery is one queued publication event.
type PolicyDelivery struct {
	ID       string
	PolicyID string
	Payload  []byte
	Attempts int
}

// MaxPolicyDeliveryAttempts bounds retries before an operator replays.
const MaxPolicyDeliveryAttempts = 50

func (r PolicyRepository) ClaimPublications(ctx context.Context) ([]PolicyDelivery, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := r.Pool.Query(ctx, `UPDATE policy_outbox SET lease_until = now() + interval '60 seconds', attempts = attempts + 1
		WHERE id IN (SELECT id FROM policy_outbox WHERE delivered_at IS NULL AND attempts < $1 AND next_attempt_at <= now()
			AND (lease_until IS NULL OR lease_until < now()) ORDER BY created_at LIMIT 10 FOR UPDATE SKIP LOCKED)
		RETURNING id, policy_id, payload, attempts`, MaxPolicyDeliveryAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PolicyDelivery
	for rows.Next() {
		var d PolicyDelivery
		if err := rows.Scan(&d.ID, &d.PolicyID, &d.Payload, &d.Attempts); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r PolicyRepository) CompletePublication(ctx context.Context, d PolicyDelivery, sendErr error) error {
	if sendErr == nil {
		_, err := r.Pool.Exec(ctx, `UPDATE policy_outbox SET delivered_at = now(), lease_until = NULL, last_error = NULL WHERE id = $1`, d.ID)
		return err
	}
	_, err := r.Pool.Exec(ctx, `UPDATE policy_outbox SET lease_until = NULL, last_error = left($2, 300),
		next_attempt_at = now() + LEAST(5 * power(2, LEAST(attempts, 6)), 300) * interval '1 second' WHERE id = $1`, d.ID, sendErr.Error())
	return err
}
