package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/adapter"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
)

type SettlementDeps struct {
	Tx         Transactor
	Settlement SettlementRepositoryPort
	Payouts    PayoutRepositoryPort
	Audit      AuditRepositoryPort
	Roles      RoleVerifier
	Orders     OrderGateway
	Vendors    VendorGateway
	Log        zerolog.Logger
	Now        func() time.Time
	// RequireApprovals (AF-19): adjustments and payout results go through
	// an approved maker-checker request only.
	RequireApprovals bool
}

// SettlementUseCase keeps the append-only ledger of what the marketplace
// owes each vendor and pays it out in manual, evidenced batches.
type SettlementUseCase struct{ SettlementDeps }

func NewSettlementUseCase(d SettlementDeps) *SettlementUseCase {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &SettlementUseCase{d}
}

// IngestVendorOrder records a completed vendor order Order reports, once.
// It posts the sale, shipping and commission entries and any refund of the
// order confirmed before it was reported.
func (uc *SettlementUseCase) IngestVendorOrder(ctx context.Context, o domain.SettlementOrder) (bool, error) {
	o.Currency = strings.ToUpper(o.Currency)
	o.PolicyVersion = domain.SettlementPolicyVersion
	if err := o.Validate(); err != nil {
		return false, err
	}
	var created bool
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		stored, isNew, err := uc.Settlement.InsertOrder(ctx, o)
		if err != nil {
			return err
		}
		if !isNew && !stored.Same(o) {
			return apperror.Conflict("This vendor order was already settled with different amounts")
		}
		created = isNew
		if err := uc.Settlement.Append(ctx, domain.SaleEntries(*stored)); err != nil {
			return err
		}
		refunds, err := uc.Settlement.SucceededRefunds(ctx, stored.VendorOrderID)
		if err != nil {
			return err
		}
		for _, r := range refunds {
			if err := uc.postRefund(ctx, stored, r.ID, r.Amount); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return false, asAppError(err)
	}
	if created {
		uc.Log.Info().Str("vendor_order_id", o.VendorOrderID).Str("vendor_id", o.VendorID).Msg("settlement_vendor_order_recorded")
	}
	return created, nil
}

// PostRefund debits a succeeded refund from its vendor order. It runs in the
// caller's transaction. A refund on a vendor order not settled yet is posted
// when Order reports the order; one with no vendor order (a late or
// duplicate capture) never reached a vendor and is not in the ledger.
func (uc *SettlementUseCase) PostRefund(ctx context.Context, refund *domain.Refund) error {
	if refund.Status != domain.RefundSucceeded || refund.VendorOrderID == nil {
		return nil
	}
	o, err := uc.Settlement.FindOrder(ctx, *refund.VendorOrderID)
	if errors.Is(err, repository.ErrSettlementOrderNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return uc.postRefund(ctx, o, refund.ID, refund.Amount)
}

func (uc *SettlementUseCase) postRefund(ctx context.Context, o *domain.SettlementOrder, refundID string, amount int64) error {
	done, err := uc.Settlement.HasEntry(ctx, domain.EntryRefund, "refund:"+refundID)
	if err != nil || done {
		return err
	}
	items, reversed, err := uc.Settlement.RefundAttribution(ctx, o.VendorOrderID)
	if err != nil {
		return err
	}
	return uc.Settlement.Append(ctx, domain.RefundEntries(*o, refundID, amount, uc.Now().UTC(), items, reversed))
}

func (uc *SettlementUseCase) requireAdmin(ctx context.Context, adminID string) error {
	if uc.Roles == nil {
		return apperror.Internal(errors.New("role verification is not configured"))
	}
	if err := uc.Roles.RequireRole(ctx, adminID, "admin"); err != nil {
		return asAppError(err)
	}
	return nil
}

func (uc *SettlementUseCase) Balances(ctx context.Context, adminID, currency string, limit, offset int) ([]repository.VendorBalance, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	out, err := uc.Settlement.Balances(ctx, strings.ToUpper(currency), limit, offset)
	return out, asAppError(err)
}

func (uc *SettlementUseCase) Statement(ctx context.Context, adminID, vendorID, currency string, limit, offset int) ([]*domain.Entry, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	out, err := uc.Settlement.Statement(ctx, vendorID, strings.ToUpper(currency), limit, offset)
	return out, asAppError(err)
}

// Adjust posts a manual correction (for example a legacy order or a debt
// written off), audited with its reason.
func (uc *SettlementUseCase) Adjust(ctx context.Context, adminID, vendorID string, amount int64, currency, reason string) (*domain.Entry, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	if uc.RequireApprovals {
		return nil, ErrApprovalRequired
	}
	return uc.adjust(ctx, adminID, vendorID, amount, currency, reason)
}

// adjust posts the correction in the caller's transaction (or its own).
func (uc *SettlementUseCase) adjust(ctx context.Context, adminID, vendorID string, amount int64, currency, reason string) (*domain.Entry, error) {
	currency, reason = strings.ToUpper(currency), strings.TrimSpace(reason)
	if err := domain.ValidateAdjustment(amount, currency, reason); err != nil {
		return nil, err
	}
	entry := domain.Entry{VendorID: vendorID, Type: domain.EntryAdjustment, Amount: amount, Currency: currency,
		SourceRef: "adjustment:" + uuid.NewString(), EligibleAt: uc.Now().UTC(), Note: &reason, CreatedBy: &adminID}
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		if err := uc.Settlement.Append(ctx, []domain.Entry{entry}); err != nil {
			return err
		}
		return uc.Audit.Record(ctx, adminID, "settlement_adjustment", "vendor", vendorID, fmt.Sprintf("%d %s: %s", amount, currency, reason))
	})
	if err != nil {
		return nil, asAppError(err)
	}
	return &entry, nil
}

// SkippedVendor explains why a vendor got no payout item.
type SkippedVendor struct {
	VendorID string `json:"vendor_id"`
	Reason   string `json:"reason"`
}

// CreatePayoutBatch builds a manual payout batch: for each vendor, every
// unpaid debit and every credit past its return window whose vendor order
// has no open return or refund, paid to the vendor's verified destination.
// The batch is idempotent by key; a vendor's entries are locked so no two
// batches include them. Order unreachable means nothing is paid.
func (uc *SettlementUseCase) CreatePayoutBatch(ctx context.Context, adminID, key, currency string, vendorIDs []string) (*domain.PayoutBatch, []SkippedVendor, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, nil, err
	}
	currency = strings.ToUpper(currency)
	if err := domain.ValidateIdempotencyKey(key); err != nil {
		return nil, nil, err
	}
	if err := domain.ValidateAdjustment(1, currency, "x"); err != nil {
		return nil, nil, err
	}
	if existing, err := uc.Payouts.FindBatchByKey(ctx, key); err == nil {
		return existing, nil, nil
	} else if !errors.Is(err, repository.ErrPayoutBatchNotFound) {
		return nil, nil, asAppError(err)
	}
	skipped := []SkippedVendor{}
	var batchID string
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		batch, created, err := uc.Payouts.CreateBatch(ctx, key, currency, adminID)
		if err != nil {
			return err
		}
		batchID = batch.ID
		if !created {
			return nil
		}
		vendors := vendorIDs
		if len(vendors) == 0 {
			if vendors, err = uc.Settlement.VendorsWithUnpaid(ctx, currency); err != nil {
				return err
			}
		}
		items := 0
		for _, vendorID := range vendors {
			reason, err := uc.addVendorItem(ctx, batch, vendorID)
			if err != nil {
				return err
			}
			if reason != "" {
				skipped = append(skipped, SkippedVendor{VendorID: vendorID, Reason: reason})
				continue
			}
			items++
		}
		if items == 0 {
			return apperror.Conflict("Nothing is payable for the selected vendors")
		}
		return uc.Audit.Record(ctx, adminID, "payout_batch_created", "payout_batch", batch.ID, fmt.Sprintf("%d item(s), %s", items, currency))
	})
	if err != nil {
		return nil, skipped, asAppError(err)
	}
	batch, err := uc.Payouts.FindBatch(ctx, batchID)
	if err != nil {
		return nil, skipped, asAppError(err)
	}
	uc.Log.Info().Str("payout_batch_id", batch.ID).Int("items", len(batch.Items)).Msg("payout_batch_created")
	return batch, skipped, nil
}

// addVendorItem adds one vendor's item, or returns why it was skipped.
func (uc *SettlementUseCase) addVendorItem(ctx context.Context, batch *domain.PayoutBatch, vendorID string) (string, error) {
	if err := uc.Payouts.LockVendor(ctx, vendorID); err != nil {
		return "", err
	}
	entries, err := uc.Settlement.Unpaid(ctx, vendorID, batch.Currency)
	if err != nil {
		return "", err
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Amount > 0 && e.VendorOrderID != nil && !seen[*e.VendorOrderID] {
			seen[*e.VendorOrderID] = true
			ids = append(ids, *e.VendorOrderID)
		}
	}
	held, err := uc.Orders.HeldVendorOrders(ctx, ids)
	if err != nil {
		return "", err
	}
	openRefunds, err := uc.Settlement.OpenRefundVendorOrders(ctx, ids)
	if err != nil {
		return "", err
	}
	now := uc.Now()
	var total int64
	pay := []string{}
	for _, e := range entries {
		isHeld := false
		if e.VendorOrderID != nil {
			_, orderHold := held[*e.VendorOrderID]
			isHeld = orderHold || openRefunds[*e.VendorOrderID]
		}
		if e.Payable(now, isHeld) {
			total += e.Amount
			pay = append(pay, e.ID)
		}
	}
	if total <= 0 {
		return "nothing_payable", nil
	}
	dest, err := uc.Vendors.DefaultDestination(ctx, vendorID)
	if errors.Is(err, adapter.ErrNoPayoutDestination) {
		return "no_verified_destination", nil
	}
	if err != nil {
		return "", err
	}
	item := &domain.PayoutItem{BatchID: batch.ID, VendorID: vendorID, Amount: total, Currency: batch.Currency,
		DestinationAccountID: dest.AccountID, DestinationVersion: dest.Version, DestinationMask: domain.MaskDestination(dest.BankBIN, dest.Last4)}
	return "", uc.Payouts.AddItem(ctx, item, pay)
}

// ResolvePayoutItem records a transfer's result. A succeeded transfer posts
// the payout debit in the same transaction; a failed one releases its
// entries for the next batch.
func (uc *SettlementUseCase) ResolvePayoutItem(ctx context.Context, adminID, itemID string, res domain.PayoutResolution) (*domain.PayoutItem, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	if uc.RequireApprovals {
		return nil, ErrApprovalRequired
	}
	return uc.resolvePayoutItem(ctx, adminID, itemID, res, nil)
}

// resolvePayoutItem records the result in the caller's transaction (or its
// own); check, when set, verifies the locked item first.
func (uc *SettlementUseCase) resolvePayoutItem(ctx context.Context, adminID, itemID string, res domain.PayoutResolution, check func(*domain.PayoutItem) error) (*domain.PayoutItem, error) {
	res.EvidenceReference, res.Note = strings.TrimSpace(res.EvidenceReference), strings.TrimSpace(res.Note)
	var item *domain.PayoutItem
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		var err error
		item, err = uc.Payouts.LockItem(ctx, itemID)
		if err != nil {
			return err
		}
		if check != nil {
			if err := check(item); err != nil {
				return err
			}
		}
		changed, err := domain.ResolvePayoutItem(item, res, adminID, uc.Now().UTC())
		if err != nil || !changed {
			return err
		}
		if err := uc.Payouts.SaveResolution(ctx, item); err != nil {
			return err
		}
		if item.Status == domain.PayoutItemSucceeded {
			note := "Payout " + item.ID
			if err := uc.Settlement.Append(ctx, []domain.Entry{{VendorID: item.VendorID, Type: domain.EntryPayout, Amount: -item.Amount,
				Currency: item.Currency, SourceRef: "payout_item:" + item.ID, EligibleAt: uc.Now().UTC(), Note: &note, CreatedBy: &adminID}}); err != nil {
				return err
			}
		}
		return uc.Audit.Record(ctx, adminID, "payout_item_"+string(item.Status), "payout_item", item.ID, firstNonEmpty(res.EvidenceReference, res.Note))
	})
	if err != nil {
		return nil, asAppError(err)
	}
	uc.Log.Info().Str("payout_item_id", item.ID).Str("vendor_id", item.VendorID).Str("status", string(item.Status)).Msg("payout_item_resolved")
	return item, nil
}

func (uc *SettlementUseCase) ListBatches(ctx context.Context, adminID string, limit, offset int) ([]*domain.PayoutBatch, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	out, err := uc.Payouts.ListBatches(ctx, limit, offset)
	return out, asAppError(err)
}

func (uc *SettlementUseCase) GetBatch(ctx context.Context, adminID, id string) (*domain.PayoutBatch, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	out, err := uc.Payouts.FindBatch(ctx, id)
	return out, asAppError(err)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return "-"
}
