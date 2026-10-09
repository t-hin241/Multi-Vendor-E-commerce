package domain

import (
	"math/big"
	"time"

	"shopee/backend/pkg/apperror"
)

// SettlementPolicyVersion names the settlement rules below so a later change
// can be told apart in the ledger.
//
// settle-v1:
//   - a completed vendor order credits its item subtotal and shipping fee and
//     debits the commission Order snapshotted at checkout;
//   - credits become payable when the return window ends (eligible_at);
//   - a confirmed refund debits the vendor in full, immediately;
//   - the commission on refunded items is given back: the refund is
//     attributed to items first (up to the item subtotal not yet refunded),
//     the reversal is floor(items * rate / 10000), capped at the commission
//     not yet reversed;
//   - commission and its reversals follow their vendor order: payable when
//     the order's sale is (after the return window, not held);
//   - refunds and negative adjustments are netted into the next payout
//     immediately, including a refund after the order was paid out (debt);
//   - a payout debits what it paid.
const SettlementPolicyVersion = "settle-v1"

type EntryType string

const (
	EntrySale               EntryType = "sale"
	EntryShipping           EntryType = "shipping"
	EntryCommission         EntryType = "commission"
	EntryRefund             EntryType = "refund"
	EntryCommissionReversal EntryType = "commission_reversal"
	EntryPayout             EntryType = "payout"
	EntryAdjustment         EntryType = "adjustment"
)

// SettlementOrder is Order's completed vendor order as snapshotted at
// checkout.
type SettlementOrder struct {
	VendorOrderID         string
	OrderID               string
	VendorID              string
	Currency              string
	SubtotalAmount        int64
	ShippingAmount        int64
	CommissionAmount      int64
	CommissionRateBps     int
	CommissionRuleVersion *int64
	CompletedAt           time.Time
	EligibleAt            time.Time
	PolicyVersion         string
}

func (o SettlementOrder) Validate() error {
	switch {
	case !currencyFormat.MatchString(o.Currency):
		return apperror.Validation("currency must be a 3-letter code")
	case o.SubtotalAmount < 0 || o.ShippingAmount < 0 || o.CommissionAmount < 0:
		return apperror.Validation("amounts must not be negative")
	case o.CommissionAmount > o.SubtotalAmount:
		return apperror.Validation("commission cannot exceed the item subtotal")
	case o.CommissionRateBps < 0 || o.CommissionRateBps > 10000:
		return apperror.Validation("commission_rate_bps must be between 0 and 10000")
	case o.EligibleAt.Before(o.CompletedAt):
		return apperror.Validation("eligible_at cannot be before completed_at")
	}
	return nil
}

// Same reports whether a replayed snapshot matches the stored one.
func (o SettlementOrder) Same(other SettlementOrder) bool {
	return o.VendorID == other.VendorID && o.OrderID == other.OrderID && o.Currency == other.Currency &&
		o.SubtotalAmount == other.SubtotalAmount && o.ShippingAmount == other.ShippingAmount &&
		o.CommissionAmount == other.CommissionAmount && o.CommissionRateBps == other.CommissionRateBps
}

// Entry is one append-only settlement ledger line.
type Entry struct {
	ID            string
	VendorID      string
	VendorOrderID *string
	Type          EntryType
	Amount        int64
	Currency      string
	BaseAmount    *int64
	SourceRef     string
	EligibleAt    time.Time
	Note          *string
	CreatedBy     *string
	CreatedAt     time.Time
}

// SaleEntries are the credits and debits of a completed vendor order.
func SaleEntries(o SettlementOrder) []Entry {
	vo := o.VendorOrderID
	source := "vendor_order:" + vo
	out := []Entry{}
	add := func(t EntryType, amount int64) {
		if amount != 0 {
			out = append(out, Entry{VendorID: o.VendorID, VendorOrderID: &vo, Type: t, Amount: amount, Currency: o.Currency, SourceRef: source, EligibleAt: o.EligibleAt})
		}
	}
	add(EntrySale, o.SubtotalAmount)
	add(EntryShipping, o.ShippingAmount)
	add(EntryCommission, -o.CommissionAmount)
	return out
}

// RefundEntries debit a confirmed refund and give back the commission on
// its item part. refundedItems and reversed are what earlier refunds of the
// same vendor order already attributed to items and reversed.
func RefundEntries(o SettlementOrder, refundID string, amount int64, now time.Time, refundedItems, reversed int64) []Entry {
	vo := o.VendorOrderID
	source := "refund:" + refundID
	items := min(amount, max(0, o.SubtotalAmount-refundedItems))
	// The refund entry keeps its item part so later refunds attribute the rest.
	out := []Entry{{VendorID: o.VendorID, VendorOrderID: &vo, Type: EntryRefund, Amount: -amount, Currency: o.Currency, BaseAmount: &items, SourceRef: source, EligibleAt: now}}
	if items == 0 || o.CommissionRateBps == 0 {
		return out
	}
	reversal := new(big.Int).Mul(big.NewInt(items), big.NewInt(int64(o.CommissionRateBps)))
	reversal.Quo(reversal, big.NewInt(10000))
	give := min(reversal.Int64(), max(0, o.CommissionAmount-reversed))
	if give > 0 {
		// The reversal is payable when the sale itself is.
		out = append(out, Entry{VendorID: o.VendorID, VendorOrderID: &vo, Type: EntryCommissionReversal, Amount: give, Currency: o.Currency,
			SourceRef: source, EligibleAt: o.EligibleAt})
	}
	return out
}

// Payable entries are what a payout may include at now: refunds and
// negative adjustments always, everything else once past its eligibility
// time and not held. Payout entries balance the entries they paid and are
// never paid again.
func (e Entry) Payable(now time.Time, held bool) bool {
	switch {
	case e.Type == EntryPayout:
		return false
	case e.Type == EntryRefund, e.Type == EntryAdjustment && e.Amount < 0:
		return true
	default:
		return !held && !e.EligibleAt.After(now)
	}
}

// ValidateAdjustment checks a manual ledger correction.
func ValidateAdjustment(amount int64, currency, reason string) error {
	if amount == 0 {
		return apperror.Validation("Adjustment amount must not be zero")
	}
	if !currencyFormat.MatchString(currency) {
		return apperror.Validation("Currency must be a 3-letter code")
	}
	if len(reason) < 1 || len(reason) > 500 {
		return apperror.Validation("Reason must be 1-500 characters")
	}
	return nil
}

type PayoutItemStatus string

const (
	PayoutItemPending   PayoutItemStatus = "pending"
	PayoutItemSucceeded PayoutItemStatus = "succeeded"
	PayoutItemFailed    PayoutItemStatus = "failed"
	// PayoutItemCancelled: claimed but never transferred; an operator took
	// it back (PW-001), and its entries are unpaid again.
	PayoutItemCancelled PayoutItemStatus = "cancelled"
)

// CancelPayoutItem takes back a pending item before any transfer. The
// reason is required; a cancelled item cannot be resolved afterwards.
func CancelPayoutItem(item *PayoutItem, reason, actor string, now time.Time) error {
	if reason == "" || len(reason) > 500 {
		return apperror.Validation("A reason of at most 500 characters is required")
	}
	if item.Status != PayoutItemPending {
		return apperror.Conflict("Only a pending payout item can be cancelled; it is " + string(item.Status))
	}
	item.Status, item.ResolvedBy, item.ResolvedAt, item.Note = PayoutItemCancelled, &actor, &now, &reason
	return nil
}

// PayoutItem is one vendor's transfer inside a manual payout batch.
type PayoutItem struct {
	ID                   string
	BatchID              string
	VendorID             string
	Amount               int64
	Currency             string
	DestinationAccountID string
	DestinationVersion   int64
	DestinationMask      string
	Status               PayoutItemStatus
	EvidenceReference    *string
	Note                 *string
	FailureReason        *string
	ResolvedBy           *string
	ResolvedAt           *time.Time
	CreatedAt            time.Time
}

type PayoutBatch struct {
	ID             string
	IdempotencyKey string
	Currency       string
	Status         string
	CreatedBy      string
	CreatedAt      time.Time
	Items          []*PayoutItem
}

// PayoutResolution is an operator recording one transfer's result.
type PayoutResolution struct {
	Outcome           PayoutItemStatus
	EvidenceReference string
	Note              string
}

// ResolvePayoutItem applies an operator's result. A succeeded transfer needs
// the bank reference; a failed one its reason. Repeating the same result is
// a no-op; changing a resolved item is refused.
func ResolvePayoutItem(item *PayoutItem, res PayoutResolution, actor string, now time.Time) (bool, error) {
	switch res.Outcome {
	case PayoutItemSucceeded:
		if res.EvidenceReference == "" || len(res.EvidenceReference) > 200 {
			return false, apperror.Validation("A succeeded payout needs the bank transfer reference (max 200 characters)")
		}
	case PayoutItemFailed:
		if res.Note == "" {
			return false, apperror.Validation("A failed payout needs the failure reason")
		}
	default:
		return false, apperror.Validation("Outcome must be succeeded or failed")
	}
	if len(res.Note) > 500 {
		return false, apperror.Validation("Note must be at most 500 characters")
	}
	if item.Status != PayoutItemPending {
		if item.Status == res.Outcome {
			return false, nil
		}
		return false, apperror.Conflict("Payout item is already " + string(item.Status))
	}
	item.Status, item.ResolvedBy, item.ResolvedAt = res.Outcome, &actor, &now
	if res.EvidenceReference != "" {
		ref := res.EvidenceReference
		item.EvidenceReference = &ref
	}
	if res.Note != "" {
		note := res.Note
		item.Note = &note
		if res.Outcome == PayoutItemFailed {
			item.FailureReason = &note
		}
	}
	return true, nil
}

// MaskDestination shows only the bank BIN and the account's last digits.
func MaskDestination(bankBIN, last4 string) string {
	return bankBIN + " ****" + last4
}

// ValidateIdempotencyKey bounds a client-generated batch key.
func ValidateIdempotencyKey(key string) error {
	if len(key) < 8 || len(key) > 128 {
		return apperror.Validation("idempotency_key must be 8-128 characters")
	}
	for _, r := range key {
		if r < 0x21 || r > 0x7e {
			return apperror.Validation("idempotency_key must be printable ASCII")
		}
	}
	return nil
}
