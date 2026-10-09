package domain

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"shopee/backend/pkg/apperror"
	casesla "shopee/backend/pkg/casesla/deadline"
)

// AF-04: a package that could not be delivered becomes a case with an
// owner and an end. Shipment reports the fact; Order holds the payout and
// an admin resolves it: a redelivery the buyer agreed to (a new attempt,
// no new charge) or a refund. A returned shipment never proves the goods
// are sellable: the shop records what came back, and only sellable units
// go back to stock.

type DeliveryExceptionStatus string

const (
	// DXInvestigating: delivery failed (attempt limit or lost); an admin
	// checks with the carrier and the buyer.
	DXInvestigating DeliveryExceptionStatus = "investigating"
	// DXAwaitingGoods: the package came back; the shop records what it
	// received, then an admin decides.
	DXAwaitingGoods DeliveryExceptionStatus = "awaiting_goods"
	// DXAwaitingBuyer: a redelivery is offered; the buyer confirms the
	// address (or declines).
	DXAwaitingBuyer DeliveryExceptionStatus = "awaiting_buyer"
	// DXRedeliveryPending: a new attempt was asked of Shipment; resolved
	// when it is delivered.
	DXRedeliveryPending DeliveryExceptionStatus = "redelivery_pending"
	// DXRefundPending: a refund was asked of Payment.
	DXRefundPending DeliveryExceptionStatus = "refund_pending"
	// DXNeedsReview: the facts disagree (delivered after all, refund
	// failed, redelivery refused, buyer declined); an admin acts.
	DXNeedsReview DeliveryExceptionStatus = "needs_review"
	DXResolved    DeliveryExceptionStatus = "resolved"
)

func (s DeliveryExceptionStatus) Open() bool { return s != DXResolved }

var deliveryExceptionTransitions = map[DeliveryExceptionStatus][]DeliveryExceptionStatus{
	DXInvestigating:     {DXAwaitingGoods, DXRefundPending, DXNeedsReview, DXResolved},
	DXAwaitingGoods:     {DXInvestigating, DXAwaitingBuyer, DXRefundPending, DXNeedsReview, DXResolved},
	DXAwaitingBuyer:     {DXRedeliveryPending, DXRefundPending, DXNeedsReview},
	DXRedeliveryPending: {DXInvestigating, DXAwaitingGoods, DXNeedsReview, DXResolved},
	DXRefundPending:     {DXAwaitingGoods, DXNeedsReview, DXResolved},
	DXNeedsReview:       {DXInvestigating, DXAwaitingGoods, DXRefundPending, DXResolved},
}

func CanTransitionDeliveryException(from, to DeliveryExceptionStatus) bool {
	for _, allowed := range deliveryExceptionTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Carrier facts Shipment reports (exception types) and outcomes Order keeps.
const (
	FactAttemptsExhausted = "attempts_exhausted"
	FactReturned          = "returned"
	FactLost              = "lost"
	OutcomeDelivered      = "delivered"
)

// ValidExceptionFact: what Shipment may report.
func ValidExceptionFact(t string) bool {
	return t == FactAttemptsExhausted || t == FactReturned || t == FactLost
}

// StatusForFact is where a fresh fact puts a case: goods coming back wait
// for the shop's receipt; anything else is investigated.
func StatusForFact(fact string) DeliveryExceptionStatus {
	if fact == FactReturned {
		return DXAwaitingGoods
	}
	return DXInvestigating
}

// Resolutions an admin chooses.
const (
	ResolutionRedelivery = "redelivery"
	ResolutionRefundDX   = "refund"
	ResolutionClosed     = "closed"
)

// DeliveryPolicyVersion is the rule set a case is decided under.
const DeliveryPolicyVersion = "delivery-resolution-v1"

// DeliveryPolicy is snapshotted on every case: the rules it opened under.
type DeliveryPolicy struct {
	Version string `json:"version"`
	// RedeliveryLimit: redeliveries per case (the first attempt excluded).
	RedeliveryLimit int `json:"redelivery_limit"`
	// FeePayer: a redelivery is never charged to the buyer in this version.
	FeePayer string `json:"redelivery_fee_payer"`
	// RefundsShipping: the refund includes the shipping fee the buyer paid
	// (the package never reached them).
	RefundsShipping bool `json:"refunds_shipping"`
}

// CurrentDeliveryPolicy is delivery-resolution-v1.
func CurrentDeliveryPolicy() DeliveryPolicy {
	return DeliveryPolicy{Version: DeliveryPolicyVersion, RedeliveryLimit: 1, FeePayer: "seller_or_marketplace", RefundsShipping: true}
}

// Destination is a delivery address snapshot (the buyer's consent).
type Destination struct {
	RecipientName string `json:"recipient_name"`
	Phone         string `json:"phone"`
	Province      string `json:"province"`
	District      string `json:"district"`
	Ward          string `json:"ward"`
	StreetAddress string `json:"street_address"`
	// AddressID is the buyer's saved address chosen, if any.
	AddressID *string `json:"address_id,omitempty"`
}

type DeliveryException struct {
	ID                    string
	OrderID               string
	VendorOrderID         string
	VendorID              string
	BuyerID               string
	ShipmentID            string
	ExceptionType         string
	CurrentShipmentID     string
	AttemptNo             int
	CarrierOutcome        string
	FailedAttempts        int
	DetectionReason       *string
	Status                DeliveryExceptionStatus
	Resolution            *string
	Policy                DeliveryPolicy
	HoldID                *string
	HoldStatus            *string
	HoldNote              *string
	RedeliveryCount       int
	ReplacementShipmentID *string
	RedeliveryAddress     *Destination
	ConsentedAt           *time.Time
	RefundID              *string
	RecoveryRef           *string
	DecidedBy             *string
	DecidedAt             *time.Time
	DecisionReason        *string
	ReviewReason          *string
	LateDeliveryAt        *time.Time
	ResolvedAt            *time.Time
	Version               int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
	// ActionDueAt and WaitingOn come from the case SLA work item (AF-07).
	ActionDueAt *time.Time
	WaitingOn   string
	// Receipt is the latest receipt for the current attempt (read model).
	Receipt *GoodsReceipt
}

// PolicyJSON is the stored snapshot.
func (d *DeliveryException) PolicyJSON() []byte {
	b, _ := json.Marshal(d.Policy)
	return b
}

// GoodsBack: the current attempt ended with the package at the shop.
func (d *DeliveryException) GoodsBack() bool { return d.CarrierOutcome == FactReturned }

// CarrierFinal: the current attempt ended without reaching the buyer
// (a refund needs this: a package still with the carrier may arrive).
func (d *DeliveryException) CarrierFinal() bool {
	return d.CarrierOutcome == FactReturned || d.CarrierOutcome == FactLost
}

// AuditDeliveryException is the admin audit entity of a case.
const AuditDeliveryException = "delivery_exception"

// DeliveryExceptionEvent is one step of a case's timeline.
type DeliveryExceptionEvent struct {
	ID          string
	ExceptionID string
	ActorID     *string
	ActorRole   string
	Action      string
	FromStatus  *string
	ToStatus    string
	Note        *string
	CreatedAt   time.Time
}

// Goods conditions the shop records per unit.
const (
	ConditionSellable = "sellable"
	ConditionDamaged  = "damaged"
	ConditionMissing  = "missing"
)

// ReceiptLine is how many units of one order item came back in a
// condition.
type ReceiptLine struct {
	OrderItemID string
	Condition   string
	Quantity    int64
}

// GoodsReceipt is what the shop received back for one attempt.
type GoodsReceipt struct {
	ID          string
	ExceptionID string
	ShipmentID  string
	Version     int
	RecordedBy  string
	ActorRole   string
	Note        *string
	Lines       []ReceiptLine
	CreatedAt   time.Time
}

// AllSellable: every unit came back sellable (a redelivery can reuse them).
func (r *GoodsReceipt) AllSellable() bool {
	for _, l := range r.Lines {
		if l.Condition != ConditionSellable {
			return false
		}
	}
	return len(r.Lines) > 0
}

// SellableByItem sums sellable units per order item.
func (r *GoodsReceipt) SellableByItem() map[string]int64 {
	out := map[string]int64{}
	for _, l := range r.Lines {
		if l.Condition == ConditionSellable {
			out[l.OrderItemID] += l.Quantity
		}
	}
	return out
}

// ValidateReceipt: every unit of the vendor order is accounted for exactly
// once (sellable + damaged + missing = ordered), never more than shipped.
func ValidateReceipt(lines []ReceiptLine, ordered map[string]int64) error {
	if len(lines) == 0 || len(lines) > 300 {
		return apperror.Validation("received_lines must list every item of the package")
	}
	got := map[string]int64{}
	seen := map[string]bool{}
	for _, l := range lines {
		if _, ok := ordered[l.OrderItemID]; !ok {
			return apperror.Validation("received_lines names an item that is not in this package")
		}
		switch l.Condition {
		case ConditionSellable, ConditionDamaged, ConditionMissing:
		default:
			return apperror.Validation("condition must be sellable, damaged or missing")
		}
		if l.Quantity < 1 {
			return apperror.Validation("quantity must be positive")
		}
		key := l.OrderItemID + "/" + l.Condition
		if seen[key] {
			return apperror.Validation("Each item and condition may appear once")
		}
		seen[key] = true
		got[l.OrderItemID] += l.Quantity
	}
	for item, qty := range ordered {
		if got[item] != qty {
			return apperror.Validation("Item " + item + ": " + strconv.FormatInt(got[item], 10) + " of " + strconv.FormatInt(qty, 10) +
				" units accounted for; record every unit as sellable, damaged or missing")
		}
	}
	return nil
}

// DeliveryRecoveryID names one item's restock for Inventory (once per
// case and item, whichever receipt version decided it).
func DeliveryRecoveryID(exceptionID, orderItemID string) string {
	return "delivery_exception:" + exceptionID + ":" + orderItemID
}

// SLAStage: an admin investigates or decides within the support
// acknowledgement window; the shop records the goods within the vendor
// response window; waiting on the buyer pauses the clock; a redelivery or
// refund in progress follows Shipment's and Payment's own deadlines.
func (d DeliveryException) SLAStage() casesla.StageInput {
	in := casesla.StageInput{ResourceType: "delivery_exception", ResourceID: d.ID, At: d.UpdatedAt, CreatedAt: d.CreatedAt,
		URL: "/admin/delivery-exceptions?exception_id=" + d.ID, WaitingOn: "admin", Duration: casesla.SupportAcknowledgement}
	switch d.Status {
	case DXInvestigating:
		in.Stage = "delivery_investigation"
	case DXAwaitingGoods:
		if d.Receipt == nil {
			in.Stage = "delivery_goods_receipt"
			in.WaitingOn = "vendor"
			in.Duration = casesla.VendorResponse
		} else {
			in.Stage = "delivery_decision"
		}
	case DXAwaitingBuyer:
		in.Stage = "delivery_buyer_consent"
		in.WaitingOn = "buyer"
		in.Pause = true
	case DXNeedsReview:
		in.Stage = "delivery_review"
	}
	return in
}

const (
	CodeDeliveryExceptionOpen apperror.Code = "delivery_exception_open"
	CodeRedeliveryOff         apperror.Code = "redelivery_disabled"
	CodeResolutionLocked      apperror.Code = "resolution_locked"
	CodeReceiptExists         apperror.Code = "receipt_exists"
)

// DeliveryExceptionOpen refuses a handover not asked for by the case.
func DeliveryExceptionOpen() *apperror.Error {
	return coded(CodeDeliveryExceptionOpen, http.StatusConflict, "A delivery exception is open for this package; only its redelivery may ship")
}

func RedeliveryDisabled() *apperror.Error {
	return coded(CodeRedeliveryOff, http.StatusConflict, "Redelivery is not enabled yet; refund the buyer instead")
}

// ResolutionLocked: the other branch was already chosen.
func ResolutionLocked() *apperror.Error {
	return coded(CodeResolutionLocked, http.StatusConflict, "A refund was already chosen for this package; it cannot be redelivered")
}

func ReceiptExists() *apperror.Error {
	return coded(CodeReceiptExists, http.StatusConflict, "The goods were already recorded; ask the marketplace to correct the receipt")
}

// DeliveryExceptionChanged: the case moved since the caller read it.
func DeliveryExceptionChanged() *apperror.Error {
	return coded(CodeVersionConflict, http.StatusConflict, "This case changed since you loaded it; reload and try again")
}
