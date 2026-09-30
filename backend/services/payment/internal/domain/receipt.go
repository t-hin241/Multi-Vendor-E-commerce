package domain

import "time"

type ReceiptStatus string

const (
	ReceiptReceived  ReceiptStatus = "received"
	ReceiptProcessed ReceiptStatus = "processed"
	ReceiptRetryable ReceiptStatus = "retryable"
	ReceiptRejected  ReceiptStatus = "rejected"
	ReceiptParked    ReceiptStatus = "parked"
)

// Receipt outcomes, recorded with the final status.
const (
	OutcomeApplied        = "applied"
	OutcomeDuplicate      = "duplicate"
	OutcomeStale          = "stale"
	OutcomeAmountMismatch = "amount_mismatch"
	OutcomeUnknownIntent  = "unknown_intent"
	OutcomeUnsupported    = "unsupported_event"
)

type EventType string

const (
	EventSucceeded EventType = "payment.succeeded"
	EventFailed    EventType = "payment.failed"
)

// Receipt is one verified provider event (a webhook delivery or a
// reconciliation finding), stored before it is applied.
type Receipt struct {
	ID                string
	Provider          string
	ProviderEventID   string
	ProviderIntentID  string
	ProviderReference string
	PaymentIntentID   *string
	EventType         EventType
	Amount            int64
	Currency          string
	FailureReason     string
	Status            ReceiptStatus
	Outcome           *string
	Attempts          int
	LastError         *string
	ReceivedAt        time.Time
	ProcessedAt       *time.Time
}

// Done receipts are never applied again.
func (r *Receipt) Done() bool { return r.Status == ReceiptProcessed || r.Status == ReceiptRejected }

// ReceiptDecision is what applying a receipt to its intent does.
type ReceiptDecision struct {
	// NewStatus is the intent's next status, or "" to leave it unchanged.
	NewStatus     Status
	FailureReason string
	ReceiptStatus ReceiptStatus
	Outcome       string
}

// DecideReceipt applies the rules for a verified event on an intent:
//   - a success must match the intent's amount and currency, otherwise it is
//     rejected for review and the intent is left untouched;
//   - a success on an already captured or refunded intent is a duplicate;
//   - a success on an expired or failed intent is still recorded as captured,
//     so money that arrived is never dropped (Order decides if it can pay);
//   - a failure never overrides a capture, and a failure after the link
//     expired is stale.
func DecideReceipt(intent *PaymentIntent, r *Receipt) ReceiptDecision {
	switch r.EventType {
	case EventSucceeded:
		if r.Amount != intent.Amount || r.Currency != intent.Currency {
			return ReceiptDecision{ReceiptStatus: ReceiptRejected, Outcome: OutcomeAmountMismatch}
		}
		if intent.Status == StatusCaptured || intent.Status == StatusRefunded {
			return ReceiptDecision{ReceiptStatus: ReceiptProcessed, Outcome: OutcomeDuplicate}
		}
		if !CanTransition(intent.Status, StatusCaptured) {
			return ReceiptDecision{ReceiptStatus: ReceiptProcessed, Outcome: OutcomeStale}
		}
		return ReceiptDecision{NewStatus: StatusCaptured, ReceiptStatus: ReceiptProcessed, Outcome: OutcomeApplied}
	case EventFailed:
		if intent.Status == StatusFailed {
			return ReceiptDecision{ReceiptStatus: ReceiptProcessed, Outcome: OutcomeDuplicate}
		}
		if !intent.Status.Open() {
			return ReceiptDecision{ReceiptStatus: ReceiptProcessed, Outcome: OutcomeStale}
		}
		reason := r.FailureReason
		if reason == "" {
			reason = "Payment declined"
		}
		return ReceiptDecision{NewStatus: StatusFailed, FailureReason: reason, ReceiptStatus: ReceiptProcessed, Outcome: OutcomeApplied}
	default:
		return ReceiptDecision{ReceiptStatus: ReceiptRejected, Outcome: OutcomeUnsupported}
	}
}

// ParkedRetryWindow is how long a receipt with no matching intent keeps
// being retried before it waits for an operator.
const ParkedRetryWindow = 24 * time.Hour
