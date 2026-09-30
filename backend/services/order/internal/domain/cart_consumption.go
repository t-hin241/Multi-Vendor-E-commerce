package domain

import "time"

// CartConsumptionStatus tracks Order's durable task of removing purchased
// lines from the buyer's cart once an order exists.
type CartConsumptionStatus string

const (
	// CartConsumptionHeld: written with the order, before stock is
	// reserved. Not yet safe to consume — the checkout may still fail.
	CartConsumptionHeld CartConsumptionStatus = "held"
	// CartConsumptionPending: the order stands; consume is due and retried
	// until Cart confirms it.
	CartConsumptionPending CartConsumptionStatus = "pending"
	// CartConsumptionConsumed: Cart returned a receipt.
	CartConsumptionConsumed CartConsumptionStatus = "consumed"
	// CartConsumptionCancelled: the checkout failed; the cart is left as is.
	CartConsumptionCancelled CartConsumptionStatus = "cancelled"
	// CartConsumptionParked: Cart permanently refused, or retries ran out.
	// Needs an operator; never blocks the order itself.
	CartConsumptionParked CartConsumptionStatus = "parked"
)

// MaxCartConsumeAttempts bounds retries before a task is parked. With the
// backoff below this spans roughly 9 hours — far inside Cart's operation
// retention window.
const MaxCartConsumeAttempts = 25

// CartConsumeLine is one purchased line of a checkout snapshot.
type CartConsumeLine struct {
	LineID   string `json:"line_id"`
	Quantity int64  `json:"quantity"`
}

// CartConsumption is persisted in the same transaction as the order, so an
// order can never exist without the task that tidies the cart afterwards.
type CartConsumption struct {
	OrderID       string
	BuyerID       string
	OperationID   string
	Lines         []CartConsumeLine
	Status        CartConsumptionStatus
	Attempts      int
	NextAttemptAt time.Time
	LastError     *string
	CreatedAt     time.Time
}

// CartConsumptionStats summarizes the consume backlog for monitoring.
type CartConsumptionStats struct {
	Held, Pending, Parked int64
	OldestPending         *time.Time
}

// CartConsumeBackoff is the delay before retry number attempts (1-based):
// 5s doubling, capped at 30 minutes.
func CartConsumeBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := 5 * time.Second
	for i := 1; i < attempts && delay < 30*time.Minute; i++ {
		delay *= 2
	}
	return min(delay, 30*time.Minute)
}
