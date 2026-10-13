package usecase

import (
	"context"
	"errors"
	"strings"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/inventory/internal/domain"
	"shopee/backend/services/inventory/internal/repository"

	"github.com/google/uuid"
)

type StockSelfServiceStore interface {
	AlertWork(context.Context, string, int) (domain.StockPage[domain.StockAlertWork], error)
	RetryAlert(context.Context, string) error
	Policy(context.Context, string) (domain.StockPolicy, error)
	LockPolicy(context.Context, string) (domain.StockPolicy, error)
	Replay(context.Context, string, string, any, any) (bool, error)
	SaveReceipt(context.Context, string, string, any, any) error
	SetPolicy(context.Context, string, domain.PolicyChange) (domain.StockPolicy, error)
	SetAlerts(context.Context, string, domain.AlertSettings) (domain.AlertSettings, error)
	Adjust(context.Context, string, string, string, domain.StockAdjustment, domain.StockPolicy) (domain.AdjustmentReceipt, error)
	Operation(context.Context, string) (domain.AdjustmentWork, domain.AdjustmentReceipt, error)
	Decide(context.Context, string, string, string, int64, int64, domain.StockPolicy) (domain.AdjustmentReceipt, error)
	Movements(context.Context, string, string, int) (domain.StockPage[domain.StockMovement], error)
	Work(context.Context, string, int) (domain.StockPage[domain.AdjustmentWork], error)
}

func (u StockSelfService) AlertWork(ctx context.Context, actor, cursor string, limit int) (domain.StockPage[domain.StockAlertWork], error) {
	if err := u.Ops.Identity.RequireRole(ctx, actor, "admin"); err != nil {
		return domain.StockPage[domain.StockAlertWork]{}, err
	}
	out, err := u.Store.AlertWork(ctx, cursor, limit)
	return out, inventoryError(err)
}
func (u StockSelfService) RetryAlert(ctx context.Context, actor, id, key, reason string) error {
	if err := u.Ops.Identity.RequireRole(ctx, actor, "admin"); err != nil {
		return err
	}
	if err := stockKey(key); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if len(reason) < 3 || len(reason) > 500 {
		return domain.StockInputError("reason must be 3-500 bytes")
	}
	return inventoryError(u.Ops.Transactions.Run(ctx, func(ctx context.Context) error {
		var receipt bool
		scope := "alert-retry:" + actor + ":" + id
		replay, err := u.Store.Replay(ctx, scope, key, reason, &receipt)
		if err != nil || replay {
			return err
		}
		if err = u.Store.RetryAlert(ctx, id); err != nil {
			return err
		}
		if err = u.Ops.audit(ctx, "stock_alert", id, actor, "stock_alert_retry", &reason, nil); err != nil {
			return err
		}
		return u.Store.SaveReceipt(ctx, scope, key, reason, true)
	}))
}

type StockSelfService struct {
	Enabled bool
	Store   StockSelfServiceStore
	Items   InventoryItemRepositoryPort
	Vendors VendorGateway
	Ops     Operations
}

func stockKey(key string) error {
	if len(key) < 1 || len(key) > 100 || strings.TrimSpace(key) != key || strings.ContainsAny(key, "\r\n\t") {
		return domain.StockInputError("Idempotency-Key is required (1-100 bytes)")
	}
	return nil
}
func (u StockSelfService) item(ctx context.Context, actor, item, permission string) (*domain.InventoryItem, error) {
	if _, err := uuid.Parse(item); err != nil {
		return nil, domain.StockInputError("item_id must be a UUID")
	}
	i, err := u.Items.FindByID(ctx, item)
	if errors.Is(err, repository.ErrItemNotFound) {
		return nil, apperror.NotFound("Stock item not found")
	}
	if err != nil {
		return nil, inventoryError(err)
	}
	if _, err = u.Vendors.GetApprovedVendorID(ctx, actor, i.VendorID, permission); err != nil {
		return nil, err
	}
	return i, nil
}
func (u StockSelfService) Policy(ctx context.Context, actor, vendor string, admin bool) (domain.StockPolicy, error) {
	if admin {
		if err := u.Ops.Identity.RequireRole(ctx, actor, "admin"); err != nil {
			return domain.StockPolicy{}, err
		}
	} else if _, err := u.Vendors.GetApprovedVendorID(ctx, actor, vendor, shopaccess.InventoryRead); err != nil {
		return domain.StockPolicy{}, err
	}
	p, err := u.Store.Policy(ctx, vendor)
	p.FeatureEnabled = u.Enabled
	return p, inventoryError(err)
}
func (u StockSelfService) Adjust(ctx context.Context, actor, key string, a domain.StockAdjustment) (out domain.AdjustmentReceipt, err error) {
	if err = a.Normalize(); err != nil {
		return out, err
	}
	if err = stockKey(key); err != nil {
		return out, err
	}
	i, err := u.item(ctx, actor, a.ItemID, shopaccess.InventoryAdjust)
	if err != nil {
		return out, err
	}
	err = u.Ops.Transactions.Run(ctx, func(ctx context.Context) error {
		p, e := u.Store.LockPolicy(ctx, i.VendorID)
		if e != nil {
			return e
		}
		// Disabling blocks new operations; committed receipts remain replayable.
		p.Enabled = p.Enabled && u.Enabled
		out, e = u.Store.Adjust(ctx, i.VendorID, actor, key, a, p)
		return e
	})
	return out, inventoryError(err)
}
func (u StockSelfService) UpdatePolicy(ctx context.Context, actor, vendor, key string, c domain.PolicyChange) (out domain.StockPolicy, err error) {
	if err = stockKey(key); err != nil {
		return out, err
	}
	c.Reason = strings.TrimSpace(c.Reason)
	if c.Limits.Receipt == 0 {
		c.Limits.Receipt = 10000
	}
	if c.ExpectedVersion < 0 || c.Limits.Receipt < 1 || c.Limits.Receipt > 10000 || len(c.Reason) < 3 || len(c.Reason) > 500 {
		return out, domain.StockInputError("limits.receipt must be 1-10000; expected_version and reason are required")
	}
	if err = u.Ops.Identity.RequireRole(ctx, actor, "admin"); err != nil {
		return out, err
	}
	scope := "policy:" + actor + ":" + vendor
	err = u.Ops.Transactions.Run(ctx, func(ctx context.Context) error {
		p, e := u.Store.LockPolicy(ctx, vendor)
		if e != nil {
			return e
		}
		replay, e := u.Store.Replay(ctx, scope, key, c, &out)
		if e != nil || replay {
			return e
		}
		if p.Version != c.ExpectedVersion {
			return domain.StockConflict("version_conflict", "Policy changed; reload before saving")
		}
		out, e = u.Store.SetPolicy(ctx, vendor, c)
		if e != nil {
			return e
		}
		if e = u.Ops.audit(ctx, "vendor", vendor, actor, "stock_self_service_policy", &c.Reason, map[string]any{"old": p, "new": out}); e != nil {
			return e
		}
		return u.Store.SaveReceipt(ctx, scope, key, c, out)
	})
	out.FeatureEnabled = u.Enabled
	return out, inventoryError(err)
}
func (u StockSelfService) Alerts(ctx context.Context, actor, item, key string, c domain.AlertSettings) (out domain.AlertSettings, err error) {
	if err = stockKey(key); err != nil {
		return out, err
	}
	if c.LowStockThreshold < 0 || c.ExpectedVersion < 1 {
		return out, domain.StockInputError("low_stock_threshold must be non-negative and expected_version positive")
	}
	i, err := u.item(ctx, actor, item, shopaccess.InventoryAdjust)
	if err != nil {
		return out, err
	}
	scope := "alerts:" + actor + ":" + item
	err = u.Ops.Transactions.Run(ctx, func(ctx context.Context) error {
		p, e := u.Store.LockPolicy(ctx, i.VendorID)
		if e != nil {
			return e
		}
		replay, e := u.Store.Replay(ctx, scope, key, c, &out)
		if e != nil || replay {
			return e
		}
		if c.Enabled && (!u.Enabled || !p.Enabled) {
			return apperror.Forbidden("Stock self-service is disabled")
		}
		out, e = u.Store.SetAlerts(ctx, item, c)
		if e != nil {
			return e
		}
		why := "Update low stock alert settings"
		if e = u.Ops.audit(ctx, "stock_item", item, actor, "stock_alert_settings", &why, map[string]any{"settings": c}); e != nil {
			return e
		}
		return u.Store.SaveReceipt(ctx, scope, key, c, out)
	})
	return out, inventoryError(err)
}
func (u StockSelfService) Operation(ctx context.Context, actor, id string) (domain.AdjustmentReceipt, error) {
	w, out, err := u.Store.Operation(ctx, id)
	if err != nil {
		return out, inventoryError(err)
	}
	if _, err = u.Vendors.GetApprovedVendorID(ctx, actor, w.VendorID, shopaccess.InventoryRead); err != nil {
		return domain.AdjustmentReceipt{}, err
	}
	return out, nil
}
func (u StockSelfService) Movements(ctx context.Context, actor, item, cursor string, limit int) (domain.StockPage[domain.StockMovement], error) {
	if _, err := u.item(ctx, actor, item, shopaccess.InventoryRead); err != nil {
		return domain.StockPage[domain.StockMovement]{}, err
	}
	out, err := u.Store.Movements(ctx, item, cursor, limit)
	return out, inventoryError(err)
}
func (u StockSelfService) Work(ctx context.Context, actor, cursor string, limit int) (domain.StockPage[domain.AdjustmentWork], error) {
	if err := u.Ops.Identity.RequireRole(ctx, actor, "admin"); err != nil {
		return domain.StockPage[domain.AdjustmentWork]{}, err
	}
	out, err := u.Store.Work(ctx, cursor, limit)
	return out, inventoryError(err)
}

type StockDecision struct {
	Decision        string `json:"decision"`
	Reason          string `json:"reason"`
	ExpectedVersion int64  `json:"expected_version"`
}

func (u StockSelfService) Decide(ctx context.Context, actor, id, key string, c StockDecision) (out domain.AdjustmentReceipt, err error) {
	if err = stockKey(key); err != nil {
		return out, err
	}
	c.Reason = strings.TrimSpace(c.Reason)
	if (c.Decision != "approve" && c.Decision != "reject" && c.Decision != "resolve") || len(c.Reason) < 3 || len(c.Reason) > 500 {
		return out, domain.StockInputError("decision must be approve, reject or resolve; reason is required")
	}
	if err = u.Ops.Identity.RequireRole(ctx, actor, "admin"); err != nil {
		return out, err
	}
	w, _, err := u.Store.Operation(ctx, id)
	if err != nil {
		return out, inventoryError(err)
	}
	// Recheck the original actor before approving, without holding DB locks.
	if c.Decision == "approve" && w.Status == "approval_required" {
		if w.FirstApprovedBy != nil {
			if err = u.Ops.Identity.RequireRole(ctx, *w.FirstApprovedBy, "admin"); err != nil {
				return out, err
			}
		}
		if _, err = u.Vendors.GetApprovedVendorID(ctx, w.ActorID, w.VendorID, shopaccess.InventoryAdjust); err != nil {
			return out, err
		}
	}
	scope := "decision:" + actor + ":" + id
	err = u.Ops.Transactions.Run(ctx, func(ctx context.Context) error {
		p, e := u.Store.LockPolicy(ctx, w.VendorID)
		if e != nil {
			return e
		}
		p.Enabled = p.Enabled && u.Enabled
		replay, e := u.Store.Replay(ctx, scope, key, c, &out)
		if e != nil || replay {
			return e
		}
		out, e = u.Store.Decide(ctx, id, actor, c.Decision, c.ExpectedVersion, u.Ops.SecondApprovalQuantity, p)
		if e != nil {
			return e
		}
		if e = u.Ops.audit(ctx, "stock_adjustment", id, actor, "stock_"+c.Decision, &c.Reason, map[string]any{"policy_version": p.Version, "movement_id": out.MovementID}); e != nil {
			return e
		}
		return u.Store.SaveReceipt(ctx, scope, key, c, out)
	})
	return out, inventoryError(err)
}
