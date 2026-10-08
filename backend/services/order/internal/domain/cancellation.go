package domain

import (
	"net/http"
	"time"

	"shopee/backend/pkg/apperror"
	casesla "shopee/backend/pkg/casesla/deadline"
)

// AF-03: a buyer asks to cancel a paid vendor order before handover, or the
// vendor reports it cannot fulfil it. The money stays paid until Payment
// confirms the refund; the vendor order is never relabelled cancelled.

type CancellationStatus string

const (
	// CancelPreparing: written, shipping fenced, payout hold being acquired.
	CancelPreparing CancellationStatus = "preparing"
	// CancelRequested: hold confirmed, waiting for an admin decision.
	CancelRequested CancellationStatus = "requested"
	// CancelStopping: approved by an admin; Shipment is being stopped.
	CancelStopping CancellationStatus = "stopping_fulfillment"
	// CancelApproved: Shipment confirmed nothing was handed over.
	CancelApproved      CancellationStatus = "approved"
	CancelRefundPending CancellationStatus = "refund_pending"
	CancelResolved      CancellationStatus = "resolved"
	CancelRejected      CancellationStatus = "rejected"
	// CancelNeedsReview: the package was already handed over, or the
	// refund failed; an admin must act.
	CancelNeedsReview CancellationStatus = "needs_review"
)

// Open requests fence the handover; rejected and resolved ones do not.
func (s CancellationStatus) Open() bool { return s != CancelRejected && s != CancelResolved }

var cancellationTransitions = map[CancellationStatus][]CancellationStatus{
	CancelPreparing:     {CancelRequested, CancelRejected},
	CancelRequested:     {CancelStopping, CancelRejected},
	CancelStopping:      {CancelApproved, CancelNeedsReview},
	CancelApproved:      {CancelRefundPending, CancelResolved},
	CancelRefundPending: {CancelResolved, CancelNeedsReview},
	CancelNeedsReview:   {CancelRefundPending, CancelRejected, CancelResolved},
}

func CanTransitionCancellation(from, to CancellationStatus) bool {
	for _, allowed := range cancellationTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Reason codes: the buyer's, then the vendor's.
var (
	BuyerCancellationReasons  = map[string]bool{"changed_mind": true, "ordered_by_mistake": true, "delivery_too_slow": true, "other": true}
	VendorCancellationReasons = map[string]bool{"out_of_stock": true, "damaged_stock": true, "cannot_fulfil": true}
)

// Stop results Shipment answers.
const (
	StopStopped    = "stopped"
	StopHandedOver = "handed_over"
	StopDelivered  = "delivered"
)

type CancellationRequest struct {
	ID             string
	OrderID        string
	VendorOrderID  string
	VendorID       string
	BuyerID        string
	Origin         string
	RequestedBy    string
	ReasonCode     string
	Reason         string
	Status         CancellationStatus
	PolicyVersion  string
	HoldID         *string
	HoldStatus     *string
	HoldNote       *string
	StopResult     *string
	Restock        *bool
	RecoveryRef    *string
	RefundID       *string
	DecidedBy      *string
	DecidedAt      *time.Time
	DecisionReason *string
	ReviewReason   *string
	ResolvedAt     *time.Time
	IdempotencyKey *string
	RequestHash    *string
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// ActionDueAt and WaitingOn come from the case SLA work item (AF-07).
	ActionDueAt *time.Time
	WaitingOn   string
}

// AuditCancellation is the admin audit entity of a request.
const AuditCancellation = "cancellation_request"

// CancellationPolicyVersion is the rule set a request is decided under.
const CancellationPolicyVersion = "paid-cancellation-v1"

// CancellationEvent is one step of a request's timeline.
type CancellationEvent struct {
	ID         string
	RequestID  string
	ActorID    *string
	ActorRole  string
	Action     string
	FromStatus *string
	ToStatus   string
	Note       *string
	CreatedAt  time.Time
}

// CheckCancellable: only a paid vendor order not yet handed over (no
// claimed handover, not shipped) can be cancelled this way.
func CheckCancellable(vo *VendorOrder, handoverClaimed bool) error {
	switch {
	case vo.Status == StatusShipped || vo.Status == StatusCompleted || handoverClaimed:
		return AlreadyShipped()
	case vo.Status != StatusPaid && vo.Status != StatusProcessing:
		return apperror.Conflict("Only a paid package that is not shipped yet can be cancelled")
	}
	return nil
}

// RecoveryID names one item's restock for Inventory (once per item).
func RecoveryID(requestID, orderItemID string) string {
	return "cancellation:" + requestID + ":" + orderItemID
}

// SLAStage: the marketplace decides within the stop-delivery window while
// the package is fenced (case-sla-v1: 4 hours); a request needing review
// gets the support acknowledgement window. Refunds have their own SLA.
func (c CancellationRequest) SLAStage() casesla.StageInput {
	in := casesla.StageInput{ResourceType: "cancellation", ResourceID: c.ID, At: c.UpdatedAt, CreatedAt: c.CreatedAt,
		URL: "/admin/cancellations?request_id=" + c.ID, WaitingOn: "admin", Duration: casesla.StopDelivery}
	switch c.Status {
	case CancelPreparing, CancelRequested, CancelStopping:
		in.Stage = "cancellation_decision"
		in.At = c.CreatedAt
	case CancelNeedsReview:
		in.Stage = "cancellation_review"
		in.Duration = casesla.SupportAcknowledgement
	}
	return in
}

const (
	CodeAlreadyShipped      apperror.Code = "already_shipped"
	CodeCancellationExists  apperror.Code = "request_exists"
	CodeCancellationPending apperror.Code = "cancellation_pending"
	CodeCancellationOff     apperror.Code = "paid_cancellation_disabled"
)

func AlreadyShipped() *apperror.Error {
	return coded(CodeAlreadyShipped, http.StatusConflict,
		"This package was already handed to the carrier; ask for a return or open a support case instead")
}

func CancellationExists() *apperror.Error {
	return coded(CodeCancellationExists, http.StatusConflict, "A cancellation request is already open for this package")
}

// CancellationPending refuses a handover while a request is open.
func CancellationPending() *apperror.Error {
	return coded(CodeCancellationPending, http.StatusConflict, "A cancellation request is open for this package; do not hand it over")
}

func CancellationDisabled() *apperror.Error {
	return coded(CodeCancellationOff, http.StatusForbidden, "Cancelling a paid order is not available yet")
}

// CancellationChanged: the request moved since the caller read it.
func CancellationChanged() *apperror.Error {
	return coded(CodeVersionConflict, http.StatusConflict, "This request changed since you loaded it; reload and try again")
}
