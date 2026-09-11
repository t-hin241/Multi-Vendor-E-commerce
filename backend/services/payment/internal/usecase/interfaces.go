package usecase

import (
	"context"

	"shopee/backend/services/payment/internal/adapter"
	"shopee/backend/services/payment/internal/domain"
)

type PaymentIntentRepositoryPort interface {
	Create(ctx context.Context, intent *domain.PaymentIntent) error
	FindByID(ctx context.Context, id string) (*domain.PaymentIntent, error)
	FindByProviderIntentID(ctx context.Context, providerIntentID string) (*domain.PaymentIntent, error)
	FindPendingByOrderID(ctx context.Context, orderID string) (*domain.PaymentIntent, error)
	UpdateStatus(ctx context.Context, id string, status domain.Status, failureReason *string) error
}

// PaymentEventRepositoryPort records every processed webhook delivery by
// its provider-assigned event id, so a retried delivery is detected and
// ignored instead of double-processing the same payment.
type PaymentEventRepositoryPort interface {
	RecordIfNew(ctx context.Context, providerEventID, paymentIntentID, eventType string) (isNew bool, err error)
}

// OrderGateway is Order's payment-facing contract: enough to size and
// authorize a payment intent, and to report a payment outcome back to the
// service that actually owns order lifecycle. Payment never decides order
// lifecycle itself — it asks Order to transition, and Order validates it.
type OrderGateway interface {
	GetOrder(ctx context.Context, orderID string) (*adapter.OrderSnapshot, error)
	MarkPaid(ctx context.Context, orderID string) error
	MarkPaymentFailed(ctx context.Context, orderID, reason string) error
}

// Simulator lets local/dev environments trigger a payment outcome without a
// real provider's hosted checkout page. Only the mock provider wiring
// supplies one; a real Stripe/PayPal deployment leaves it nil, and the
// "simulate" endpoint responds accordingly instead of ever being reachable
// in production.
type Simulator interface {
	BuildSignedEvent(providerIntentID string, amount int64, currency string, succeeded bool, failureReason string) (payload []byte, signature string, err error)
}
