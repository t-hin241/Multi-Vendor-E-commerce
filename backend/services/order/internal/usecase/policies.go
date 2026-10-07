package usecase

import (
	"context"
	"errors"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// PolicyVersionPort is Order's read model of published policies and the
// policy snapshots of orders (AF-02).
type PolicyVersionPort interface {
	Insert(ctx context.Context, v domain.PolicyVersion) (bool, error)
	Find(ctx context.Context, policyID string) (*domain.PolicyVersion, error)
	MarketplaceAt(ctx context.Context, t time.Time) ([]domain.PolicyVersion, error)
	ShopsAt(ctx context.Context, vendorIDs []string, t time.Time) (map[string]domain.PolicyVersion, error)
	OrderSnapshot(ctx context.Context, orderID string) (*domain.OrderPolicySnapshot, map[string]*domain.VendorPolicySnapshot, error)
	VendorSnapshot(ctx context.Context, vendorOrderID string) (*domain.VendorPolicySnapshot, error)
}

// policiesOn reports whether new orders snapshot their policies
// (FEATURE_VERSIONED_POLICIES_ENABLED).
func (uc *OrderUseCase) policiesOn() bool {
	return uc.VersionedPolicies && uc.Policies != nil
}

// ApplyPolicyPublished stores a version Vendor published. Versions are
// immutable: a replay is a no-op, a different payload under the same id is
// refused for review.
func (uc *OrderUseCase) ApplyPolicyPublished(ctx context.Context, v domain.PolicyVersion) error {
	if uc.Policies == nil {
		return apperror.Internal(errors.New("policy read model is not configured"))
	}
	if err := domain.ValidatePolicyVersion(v); err != nil {
		return err
	}
	v.EffectiveAt = v.EffectiveAt.UTC()
	inserted, err := uc.Policies.Insert(ctx, v)
	if err != nil {
		return appError(err)
	}
	if inserted {
		uc.Log.Info().Str("policy_id", v.PolicyID).Str("scope", v.Scope).Str("kind", v.Kind).Int64("version", v.Version).Msg("order_policy_version_stored")
		return nil
	}
	stored, err := uc.Policies.Find(ctx, v.PolicyID)
	if err != nil {
		return appError(err)
	}
	if stored == nil || stored.ContentHash != v.ContentHash || stored.Version != v.Version || !stored.EffectiveAt.Equal(v.EffectiveAt) {
		return apperror.Conflict("A different publication already exists for this policy version")
	}
	return nil
}

// PolicyRuleReadiness answers Vendor: does Order enforce this rule version?
func (uc *OrderUseCase) PolicyRuleReadiness(key, value string) (bool, string, string) {
	return domain.RuleReadiness(key, value)
}

// currentPolicies is the marketplace snapshot a checkout at t gets.
func (uc *OrderUseCase) currentPolicies(ctx context.Context, t time.Time) (*domain.OrderPolicySnapshot, error) {
	versions, err := uc.Policies.MarketplaceAt(ctx, t)
	if err != nil {
		return nil, appError(err)
	}
	return domain.BuildPolicySnapshot(domain.ActiveAt(versions, t), uc.ReturnPolicy, t)
}

// snapshotPolicies fixes the plan's policies at t: the marketplace
// versions (which must be those the buyer accepted, when given) and each
// shop's approved policy.
func (uc *OrderUseCase) snapshotPolicies(ctx context.Context, plan *domain.Plan, accepted map[string]int64, t time.Time) error {
	snapshot, err := uc.currentPolicies(ctx, t)
	if err != nil {
		return err
	}
	if accepted != nil && !domain.SameVersions(accepted, snapshot.VersionsByKind()) {
		return domain.PolicyChanged()
	}
	shops, err := uc.Policies.ShopsAt(ctx, plan.VendorIDs(), t)
	if err != nil {
		return appError(err)
	}
	plan.OrderPolicy = snapshot
	plan.VendorPolicies = make([]*domain.VendorPolicySnapshot, len(plan.VendorOrders))
	for i, vo := range plan.VendorOrders {
		var shop *domain.PolicyVersion
		if v, ok := shops[vo.VendorID]; ok {
			shop = &v
		}
		plan.VendorPolicies[i] = snapshot.ForVendor(shop)
	}
	return nil
}

// returnPolicyFor is the return rule a vendor order was sold under: its
// snapshot, or the configured (legacy) rule for orders placed before.
func (uc *OrderUseCase) returnPolicyFor(ctx context.Context, vendorOrderID string) (domain.ReturnPolicy, error) {
	if uc.Policies == nil {
		return uc.ReturnPolicy, nil
	}
	snapshot, err := uc.Policies.VendorSnapshot(ctx, vendorOrderID)
	if err != nil {
		return domain.ReturnPolicy{}, appError(err)
	}
	if snapshot == nil {
		return uc.ReturnPolicy, nil
	}
	return snapshot.ReturnPolicy(), nil
}

// OrderPolicyView is an order's policy snapshot as the caller may see it.
type OrderPolicyView struct {
	OrderID string
	// Legacy: placed before snapshots; the configured rules applied.
	Legacy       bool
	Order        *domain.OrderPolicySnapshot
	VendorOrders map[string]*domain.VendorPolicySnapshot
}

// PolicySnapshot returns the policies an order was placed under: the
// buyer sees their own order, a shop only its vendor orders, an admin all.
func (uc *OrderUseCase) PolicySnapshot(ctx context.Context, actorID, role, orderID string) (*OrderPolicyView, error) {
	if uc.Policies == nil {
		return nil, apperror.NotFound("Order not found")
	}
	order, err := uc.findOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	snapshot, vendors, err := uc.Policies.OrderSnapshot(ctx, order.ID)
	if err != nil {
		return nil, notFoundOrInternal(err, repository.ErrOrderNotFound, "Order not found")
	}
	view := &OrderPolicyView{OrderID: order.ID, Legacy: snapshot == nil, Order: snapshot, VendorOrders: vendors}
	switch role {
	case "buyer":
		if order.BuyerID != actorID {
			return nil, apperror.NotFound("Order not found")
		}
	case "admin":
	case "vendor":
		vos, err := uc.VendorOrders.ListByOrderID(ctx, order.ID)
		if err != nil {
			return nil, appError(err)
		}
		own := map[string]*domain.VendorPolicySnapshot{}
		for _, vo := range vos {
			_, err := uc.Vendors.GetApprovedVendorID(ctx, actorID, vo.VendorID, shopaccess.OrdersRead)
			if shopaccess.IsUnavailable(err) {
				return nil, err
			}
			if err == nil {
				own[vo.ID] = vendors[vo.ID]
			}
		}
		if len(own) == 0 {
			return nil, apperror.NotFound("Order not found")
		}
		view.VendorOrders = own
	default:
		return nil, apperror.NotFound("Order not found")
	}
	return view, nil
}
