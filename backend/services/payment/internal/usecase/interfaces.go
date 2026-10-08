package usecase

import (
	"context"
	"time"

	"shopee/backend/services/payment/internal/adapter"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/provider"
	"shopee/backend/services/payment/internal/repository"
)

// Transactor runs writes of several repositories in one transaction.
type Transactor interface {
	Run(ctx context.Context, fn func(ctx context.Context) error) error
}

type PaymentIntentRepositoryPort interface {
	NextOrderCode(ctx context.Context) (int64, error)
	CreateOperation(ctx context.Context, intent *domain.PaymentIntent) error
	MarkLinked(ctx context.Context, id string, link provider.CreateIntentResult) error
	RecordCreateFailure(ctx context.Context, id, message string) error
	Close(ctx context.Context, id, reason string) error
	Transition(ctx context.Context, id string, from, to domain.Status, failureReason *string) error
	Touch(ctx context.Context, id string) error
	FindByID(ctx context.Context, id string) (*domain.PaymentIntent, error)
	LockForEvent(ctx context.Context, providerName, providerIntentID, reference string) (*domain.PaymentIntent, error)
	FindOpenByOrderID(ctx context.Context, orderID string) (*domain.PaymentIntent, error)
	ListForReconciliation(ctx context.Context, creatingAfter, expiredAfter time.Duration, limit int) ([]*domain.PaymentIntent, error)
	Search(ctx context.Context, q string, limit int) ([]*domain.PaymentIntent, error)
	Counts(ctx context.Context) (creating, expiredOpen int64, err error)
}

// ReceiptRepositoryPort stores verified provider events before they are
// applied, so none is lost between receiving and applying it.
type ReceiptRepositoryPort interface {
	Record(ctx context.Context, r *domain.Receipt) (*domain.Receipt, bool, error)
	Lock(ctx context.Context, id string) (*domain.Receipt, error)
	FindByID(ctx context.Context, id string) (*domain.Receipt, error)
	Finish(ctx context.Context, id string, intentID *string, status domain.ReceiptStatus, outcome string) error
	Park(ctx context.Context, id string) error
	MarkRetryable(ctx context.Context, id, message string) error
	Requeue(ctx context.Context, id string) error
	ListDue(ctx context.Context, limit int) ([]*domain.Receipt, error)
	ListByStatus(ctx context.Context, status domain.ReceiptStatus, limit, offset int) ([]*domain.Receipt, error)
	Search(ctx context.Context, q string, limit int) ([]*domain.Receipt, error)
	Counts(ctx context.Context) (map[string]int64, error)
	RecordLegacyEvent(ctx context.Context, providerEventID, intentID string, eventType domain.EventType) error
}

// OrderGateway is Order's payment-facing contract. Payment never decides
// order lifecycle itself; outcomes reach Order through the sync outbox.
type OrderGateway interface {
	GetOrder(ctx context.Context, orderID string) (*adapter.OrderSnapshot, error)
	HeldVendorOrders(ctx context.Context, vendorOrderIDs []string) (map[string]string, error)
}

// VendorGateway resolves a vendor's verified payout destination (masked).
type VendorGateway interface {
	DefaultDestination(ctx context.Context, vendorID string) (*adapter.PayoutDestination, error)
}

// Simulator lets local/dev environments trigger a payment outcome without a
// real provider's hosted checkout page. Only the mock provider supplies one.
type Simulator interface {
	BuildSignedEvent(providerIntentID string, amount int64, currency string, succeeded bool, failureReason string) (payload []byte, signature string, err error)
}

// RefundRepositoryPort persists refunds. Request and Resolve run their
// callback inside the transaction that locks the capture or the refund, so
// the domain decision and the write cannot interleave with another one.
type RefundRepositoryPort interface {
	Request(ctx context.Context, req domain.RefundRequest, check func(intent *domain.PaymentIntent, committed int64) error) (*domain.Refund, bool, error)
	FindByID(ctx context.Context, id string) (*domain.Refund, error)
	List(ctx context.Context, status string, limit, offset int) ([]*domain.Refund, int, error)
	Search(ctx context.Context, q string, limit int) ([]*domain.Refund, error)
	Resolve(ctx context.Context, id string, resolve func(*domain.Refund) (bool, error)) (*domain.Refund, error)
}

// RoleVerifier re-checks with Identity that an actor still holds a role
// before a money-moving admin action.
type RoleVerifier interface {
	RequireRole(ctx context.Context, userID, role string) error
}

type SettlementRepositoryPort interface {
	InsertOrder(ctx context.Context, o domain.SettlementOrder) (*domain.SettlementOrder, bool, error)
	FindOrder(ctx context.Context, vendorOrderID string) (*domain.SettlementOrder, error)
	Append(ctx context.Context, entries []domain.Entry) error
	RefundAttribution(ctx context.Context, vendorOrderID string) (refundedItems, reversed int64, err error)
	HasEntry(ctx context.Context, t domain.EntryType, source string) (bool, error)
	SucceededRefunds(ctx context.Context, vendorOrderID string) ([]repository.SettledRefund, error)
	OpenRefundVendorOrders(ctx context.Context, ids []string) (map[string]bool, error)
	// ActiveHolds: vendor orders with an active settlement hold (00 §6.1).
	ActiveHolds(ctx context.Context, ids []string) (map[string]bool, error)
	Unpaid(ctx context.Context, vendorID, currency string) ([]*domain.Entry, error)
	VendorsWithUnpaid(ctx context.Context, currency string) ([]string, error)
	Statement(ctx context.Context, vendorID, currency string, limit, offset int) ([]*domain.Entry, error)
	Balances(ctx context.Context, currency string, limit, offset int) ([]repository.VendorBalance, error)
}

type PayoutRepositoryPort interface {
	LockVendor(ctx context.Context, vendorID string) error
	CreateBatch(ctx context.Context, key, currency, actor string) (*domain.PayoutBatch, bool, error)
	FindBatch(ctx context.Context, id string) (*domain.PayoutBatch, error)
	FindBatchByKey(ctx context.Context, key string) (*domain.PayoutBatch, error)
	AddItem(ctx context.Context, item *domain.PayoutItem, entryIDs []string) error
	LockItem(ctx context.Context, id string) (*domain.PayoutItem, error)
	SaveResolution(ctx context.Context, item *domain.PayoutItem) error
	ListBatches(ctx context.Context, limit, offset int) ([]*domain.PayoutBatch, error)
}

type AuditRepositoryPort interface {
	Record(ctx context.Context, actor, action, targetType, targetID, reason string) error
	ForTarget(ctx context.Context, targetType, targetID string) ([]repository.AuditEntry, error)
}
