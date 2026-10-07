package usecase

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/events"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/repository"
)

// PolicyRepositoryPort stores policy versions, their publication outbox and
// the marketplace policy audit.
type PolicyRepositoryPort interface {
	CreateMarketplace(ctx context.Context, p *domain.MarketplacePolicy) error
	FindMarketplace(ctx context.Context, id string) (*domain.MarketplacePolicy, error)
	ListMarketplace(ctx context.Context, kind, status string, limit, offset int) ([]*domain.MarketplacePolicy, error)
	SaveMarketplaceStatus(ctx context.Context, p *domain.MarketplacePolicy) error
	ListPreparing(ctx context.Context, limit int) ([]*domain.MarketplacePolicy, error)
	ActiveMarketplace(ctx context.Context, now time.Time) ([]*domain.MarketplacePolicy, error)
	PublishedHistory(ctx context.Context, kind domain.PolicyKind) ([]*domain.MarketplacePolicy, error)
	PublishedVersion(ctx context.Context, kind domain.PolicyKind, version int) (*domain.MarketplacePolicy, error)
	RecordPolicyAudit(ctx context.Context, policyID, actorID, action string, reason *string) error
	QueueMarketplaceNotices(ctx context.Context, seq int64) error
	CreateShopProposal(ctx context.Context, p *domain.ShopPolicy) error
	FindShopPolicy(ctx context.Context, id string) (*domain.ShopPolicy, error)
	ListShopPolicies(ctx context.Context, vendorID string) ([]*domain.ShopPolicy, error)
	ListShopProposals(ctx context.Context, status string, limit, offset int) ([]*domain.ShopPolicy, error)
	DecideShopPolicy(ctx context.Context, p *domain.ShopPolicy) error
	ApprovedShopPolicy(ctx context.Context, vendorID string) (*domain.ShopPolicy, error)
	QueuePublication(ctx context.Context, policyID string, payload any) error
}

// RuleReadinessChecker asks the service that enforces a rule whether it
// enforces this version. An unreachable owner is "not ready", never ready.
type RuleReadinessChecker interface {
	Readiness(ctx context.Context, owner, key, value string) domain.RuleReadiness
}

// PolicyUseCase manages versioned policies. Vendor owns the text; rule
// owners acknowledge the rules it cites before it becomes public.
type PolicyUseCase struct {
	Policies PolicyRepositoryPort
	Vendors  VendorRepositoryPort
	Audit    AuditLogRepositoryPort
	Notices  NoticeQueue
	Rules    RuleReadinessChecker
	Ops      Operations
	// Enabled is FEATURE_VERSIONED_POLICIES_ENABLED: off, drafts can still
	// be written but nothing is published or shown from the new tables.
	Enabled bool
	Now     func() time.Time
	Log     zerolog.Logger
}

func (uc *PolicyUseCase) now() time.Time {
	if uc.Now != nil {
		return uc.Now()
	}
	return time.Now()
}

func (uc *PolicyUseCase) requireAdmin(ctx context.Context, adminID string) error {
	return wrap(uc.Ops.Actors.RequireRole(ctx, adminID, "admin"))
}

// CreateDraft records a new marketplace policy version. Its text and rule
// references are final: a correction is another draft.
func (uc *PolicyUseCase) CreateDraft(ctx context.Context, adminID string, d domain.PolicyInput) (*domain.MarketplacePolicy, error) {
	p, err := domain.NewMarketplacePolicy(d, adminID)
	if err != nil {
		return nil, err
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	err = uc.Ops.Tx.Run(ctx, func(ctx context.Context) error {
		if err := uc.Policies.CreateMarketplace(ctx, p); err != nil {
			return err
		}
		return uc.Policies.RecordPolicyAudit(ctx, p.ID, adminID, "policy_drafted", nil)
	})
	return p, wrap(err)
}

func (uc *PolicyUseCase) ListMarketplace(ctx context.Context, adminID, kind, status string, limit, offset int) ([]*domain.MarketplacePolicy, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	if kind != "" {
		if _, err := domain.ParsePolicyKind(kind); err != nil {
			return nil, err
		}
	}
	switch domain.PolicyStatus(status) {
	case "", domain.PolicyDraft, domain.PolicyPreparing, domain.PolicyPublished, domain.PolicyWithdrawn:
	default:
		return nil, apperror.Validation("Invalid status filter")
	}
	items, err := uc.Policies.ListMarketplace(ctx, kind, status, limit, offset)
	return items, wrap(err)
}

// Publish asks every rule owner to acknowledge the rule versions the policy
// cites, then publishes it. While an owner is not ready the version stays
// preparing and the previous version stays public; the worker asks again.
// It returns published=false for a version still preparing (202).
func (uc *PolicyUseCase) Publish(ctx context.Context, adminID, policyID string, expectedVersion int64, reason string) (*domain.MarketplacePolicy, bool, error) {
	if !uc.Enabled {
		return nil, false, domain.PoliciesDisabled()
	}
	note, err := requiredReason(reason)
	if err != nil {
		return nil, false, err
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, false, err
	}
	var p *domain.MarketplacePolicy
	err = uc.Ops.Tx.Run(ctx, func(ctx context.Context) error {
		current, err := uc.findMarketplace(ctx, policyID)
		if err != nil {
			return err
		}
		if current.RowVersion != expectedVersion {
			return domain.VersionConflict()
		}
		if err := current.CanPublish(uc.now()); err != nil {
			return err
		}
		if current.Status == domain.PolicyDraft {
			at := uc.now().UTC()
			current.Status, current.PreparingSince = domain.PolicyPreparing, &at
		}
		current.PublicationReason, current.PublishedBy = note, &adminID
		if err := uc.saveStatus(ctx, current); err != nil {
			return err
		}
		p = current
		return uc.Policies.RecordPolicyAudit(ctx, current.ID, adminID, "policy_publication_requested", note)
	})
	if err != nil {
		return nil, false, wrap(err)
	}
	p, err = uc.completePublication(ctx, p)
	if err != nil {
		return nil, false, wrap(err)
	}
	return p, p.Status == domain.PolicyPublished, nil
}

// completePublication collects the rule owners' answers outside any
// transaction, then publishes if every rule is acknowledged.
func (uc *PolicyUseCase) completePublication(ctx context.Context, p *domain.MarketplacePolicy) (*domain.MarketplacePolicy, error) {
	readiness := map[string]domain.RuleReadiness{}
	for key, value := range p.RuleRefs {
		readiness[key] = uc.Rules.Readiness(ctx, domain.RuleOwner(key), key, value)
	}
	var result *domain.MarketplacePolicy
	err := uc.Ops.Tx.Run(ctx, func(ctx context.Context) error {
		current, err := uc.findMarketplace(ctx, p.ID)
		if err != nil {
			return err
		}
		if current.Status != domain.PolicyPreparing || current.RowVersion != p.RowVersion {
			result = current // withdrawn or published meanwhile
			return nil
		}
		changed := !reflect.DeepEqual(current.Readiness, readiness)
		current.Readiness = readiness
		if !changed && !current.AllReady() {
			result = current // same answers: keep the row version admins see
			return nil
		}
		if current.AllReady() {
			at := uc.now().UTC()
			current.Status, current.PublishedAt = domain.PolicyPublished, &at
		}
		if err := uc.saveStatus(ctx, current); err != nil {
			return err
		}
		result = current
		if current.Status != domain.PolicyPublished {
			return nil
		}
		if err := uc.Policies.QueuePublication(ctx, current.ID, events.PolicyPublication{PolicyID: current.ID, Scope: "marketplace",
			Kind: string(current.Kind), Version: int64(current.Version), ContentHash: current.ContentHash, RuleRefs: current.RuleRefs,
			EffectiveAt: current.EffectiveAt.UTC()}); err != nil {
			return err
		}
		if err := uc.Policies.QueueMarketplaceNotices(ctx, current.Seq); err != nil {
			return err
		}
		actor := current.CreatedBy
		if current.PublishedBy != nil {
			actor = *current.PublishedBy
		}
		return uc.Policies.RecordPolicyAudit(ctx, current.ID, actor, "policy_published", current.PublicationReason)
	})
	if err != nil {
		return nil, err
	}
	if result.Status == domain.PolicyPublished {
		uc.Log.Info().Str("policy_id", result.ID).Str("kind", string(result.Kind)).Int("version", result.Version).Msg("vendor_policy_published")
	}
	return result, nil
}

// RetryPreparing asks the rule owners again for every preparing version
// (worker); each version is independent.
func (uc *PolicyUseCase) RetryPreparing(ctx context.Context, limit int) (int, error) {
	if !uc.Enabled {
		return 0, nil
	}
	items, err := uc.Policies.ListPreparing(ctx, limit)
	if err != nil {
		return 0, err
	}
	published := 0
	for _, p := range items {
		done, err := uc.completePublication(ctx, p)
		if err != nil {
			uc.Log.Error().Err(err).Str("policy_id", p.ID).Msg("vendor_policy_publication_retry_failed")
			continue
		}
		if done.Status == domain.PolicyPublished {
			published++
		} else if p.PreparingSince != nil && uc.now().Sub(*p.PreparingSince) > time.Hour {
			uc.Log.Warn().Str("policy_id", p.ID).Str("kind", string(p.Kind)).Msg("vendor_policy_preparing_overdue")
		}
	}
	return published, nil
}

// Withdraw gives up a draft or a preparing version.
func (uc *PolicyUseCase) Withdraw(ctx context.Context, adminID, policyID string, expectedVersion int64, reason string) (*domain.MarketplacePolicy, error) {
	note, err := requiredReason(reason)
	if err != nil {
		return nil, err
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	var p *domain.MarketplacePolicy
	err = uc.Ops.Tx.Run(ctx, func(ctx context.Context) error {
		current, err := uc.findMarketplace(ctx, policyID)
		if err != nil {
			return err
		}
		if current.RowVersion != expectedVersion {
			return domain.VersionConflict()
		}
		if current.Status != domain.PolicyDraft && current.Status != domain.PolicyPreparing {
			return apperror.Conflict("A published version cannot be withdrawn; publish a new version instead")
		}
		current.Status = domain.PolicyWithdrawn
		if err := uc.saveStatus(ctx, current); err != nil {
			return err
		}
		p = current
		return uc.Policies.RecordPolicyAudit(ctx, current.ID, adminID, "policy_withdrawn", note)
	})
	return p, wrap(err)
}

// ActivePolicies are the versions in force now, for the public pages.
func (uc *PolicyUseCase) ActivePolicies(ctx context.Context) ([]*domain.MarketplacePolicy, error) {
	if !uc.Enabled {
		return nil, apperror.NotFound("Policies are not published yet")
	}
	items, err := uc.Policies.ActiveMarketplace(ctx, uc.now())
	return items, wrap(err)
}

// PolicyHistory lists a kind's published versions (old links stay valid).
func (uc *PolicyUseCase) PolicyHistory(ctx context.Context, kind string) ([]*domain.MarketplacePolicy, error) {
	k, err := domain.ParsePolicyKind(kind)
	if err != nil {
		return nil, err
	}
	if !uc.Enabled {
		return nil, apperror.NotFound("Policies are not published yet")
	}
	items, err := uc.Policies.PublishedHistory(ctx, k)
	return items, wrap(err)
}

// PolicyVersion returns one published version.
func (uc *PolicyUseCase) PolicyVersion(ctx context.Context, kind string, version int) (*domain.MarketplacePolicy, error) {
	k, err := domain.ParsePolicyKind(kind)
	if err != nil {
		return nil, err
	}
	if !uc.Enabled {
		return nil, apperror.NotFound("Policy version not found")
	}
	p, err := uc.Policies.PublishedVersion(ctx, k, version)
	if errors.Is(err, repository.ErrPolicyNotFound) {
		return nil, apperror.NotFound("Policy version not found")
	}
	return p, wrap(err)
}

// ProposeShopPolicy records the shop's proposed text; it is public only
// once an admin approves it. Text that lowers the marketplace's minimum
// protection is refused.
func (uc *PolicyUseCase) ProposeShopPolicy(ctx context.Context, userID, vendorID, content string) (*domain.ShopPolicy, error) {
	text, err := domain.ValidateShopPolicy(content)
	if err != nil {
		return nil, err
	}
	if err := uc.Ops.Actors.RequireRole(ctx, userID, "vendor"); err != nil {
		return nil, wrap(err)
	}
	p := &domain.ShopPolicy{VendorID: vendorID, Content: text, ContentHash: domain.TextHash(text), ProposedBy: &userID}
	err = uc.Ops.Tx.Run(ctx, func(ctx context.Context) error {
		if _, err := getOwnedVendor(ctx, uc.Vendors, userID, vendorID); err != nil {
			return err
		}
		return uc.Policies.CreateShopProposal(ctx, p)
	})
	return p, wrap(err)
}

func (uc *PolicyUseCase) ListShopPolicies(ctx context.Context, userID, vendorID string) ([]*domain.ShopPolicy, error) {
	if err := uc.Ops.Actors.RequireRole(ctx, userID, "vendor"); err != nil {
		return nil, wrap(err)
	}
	if _, err := getOwnedVendor(ctx, uc.Vendors, userID, vendorID); err != nil {
		return nil, err
	}
	items, err := uc.Policies.ListShopPolicies(ctx, vendorID)
	return items, wrap(err)
}

func (uc *PolicyUseCase) ListShopProposals(ctx context.Context, adminID, status string, limit, offset int) ([]*domain.ShopPolicy, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	switch domain.ShopPolicyStatus(status) {
	case "", domain.ShopPolicyProposed, domain.ShopPolicyApproved, domain.ShopPolicyRejected:
	default:
		return nil, apperror.Validation("Invalid status filter")
	}
	items, err := uc.Policies.ListShopProposals(ctx, status, limit, offset)
	return items, wrap(err)
}

// DecideShopPolicy approves or rejects a proposal (a rejection needs a
// reason). An approval publishes the shop policy at once; legacy text is
// checked against the marketplace minimum like any proposal.
func (uc *PolicyUseCase) DecideShopPolicy(ctx context.Context, adminID, proposalID string, approve bool, reason string) (*domain.ShopPolicy, error) {
	var note *string
	if r := strings.TrimSpace(reason); r != "" {
		if len([]rune(r)) > 500 {
			return nil, apperror.Validation("Reason must be at most 500 characters")
		}
		note = &r
	} else if !approve {
		return nil, apperror.Validation("A rejection reason is required")
	}
	if approve && !uc.Enabled {
		return nil, domain.PoliciesDisabled()
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	var p *domain.ShopPolicy
	err := uc.Ops.Tx.Run(ctx, func(ctx context.Context) error {
		current, err := uc.Policies.FindShopPolicy(ctx, proposalID)
		if errors.Is(err, repository.ErrPolicyNotFound) {
			return apperror.NotFound("Shop policy proposal not found")
		}
		if err != nil {
			return err
		}
		if current.Status != domain.ShopPolicyProposed {
			return apperror.Conflict("This proposal was already decided")
		}
		if approve {
			if _, err := domain.ValidateShopPolicy(current.Content); err != nil {
				return err
			}
		}
		vendor, err := uc.Vendors.FindByID(ctx, current.VendorID)
		if err != nil {
			return err
		}
		at := uc.now().UTC()
		current.DecidedBy, current.DecisionReason, current.DecidedAt = &adminID, note, &at
		current.Status = domain.ShopPolicyRejected
		action := "shop_policy_rejected"
		if approve {
			current.Status, action = domain.ShopPolicyApproved, "shop_policy_approved"
		}
		if err := uc.Policies.DecideShopPolicy(ctx, current); err != nil {
			if errors.Is(err, repository.ErrStaleVersion) {
				return apperror.Conflict("This proposal was already decided")
			}
			return err
		}
		if err := uc.Audit.Create(ctx, current.VendorID, adminID, action, note, int64(current.Version)); err != nil {
			return err
		}
		if err := uc.Notices.Queue(ctx, vendor.ID, vendor.UserID, action, current.Seq); err != nil {
			return err
		}
		p = current
		if !approve {
			return nil
		}
		return uc.Policies.QueuePublication(ctx, current.ID, events.PolicyPublication{PolicyID: current.ID, Scope: "shop",
			VendorID: current.VendorID, Kind: "shop", Version: int64(current.Version), ContentHash: current.ContentHash, EffectiveAt: at})
	})
	return p, wrap(err)
}

// PublicShopPolicy is the approved policy shown on the shop page (nil when
// none); with the feature off the legacy free text is shown instead.
func (uc *PolicyUseCase) PublicShopPolicy(ctx context.Context, vendorID string) (*domain.ShopPolicy, error) {
	p, err := uc.Policies.ApprovedShopPolicy(ctx, vendorID)
	return p, wrap(err)
}

func (uc *PolicyUseCase) findMarketplace(ctx context.Context, id string) (*domain.MarketplacePolicy, error) {
	p, err := uc.Policies.FindMarketplace(ctx, id)
	if errors.Is(err, repository.ErrPolicyNotFound) {
		return nil, apperror.NotFound("Policy version not found")
	}
	return p, err
}

func (uc *PolicyUseCase) saveStatus(ctx context.Context, p *domain.MarketplacePolicy) error {
	if err := uc.Policies.SaveMarketplaceStatus(ctx, p); err != nil {
		if errors.Is(err, repository.ErrStaleVersion) {
			return domain.VersionConflict()
		}
		return err
	}
	return nil
}

func requiredReason(reason string) (*string, error) {
	r := strings.TrimSpace(reason)
	if r == "" || len([]rune(r)) > 500 {
		return nil, apperror.Validation("A reason of at most 500 characters is required")
	}
	return &r, nil
}
