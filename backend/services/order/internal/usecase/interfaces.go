package usecase

import (
	"context"
	"io"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/eventbus"

	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

type OrderRepositoryPort interface {
	WithLockedOrder(context.Context, string, func(context.Context) error) error
	CreateFromPlan(ctx context.Context, plan *domain.Plan) (*domain.Order, error)
	FindByID(ctx context.Context, id string) (*domain.Order, error)
	List(ctx context.Context, f repository.OrderFilter, limit, offset int) ([]*domain.Order, int64, error)
	TransitionStatus(ctx context.Context, id string, from, to domain.Status, reason *string) error
	SetCheckoutState(ctx context.Context, id string, state domain.CheckoutState) error
	AddRefunded(ctx context.Context, id string, amount int64) error
	ListItemsByOrder(ctx context.Context, orderID string) ([]*domain.OrderItem, error)
	FindItem(ctx context.Context, orderID, itemID string) (*domain.OrderItem, error)
	ListReviewEligibility(ctx context.Context, buyerID, productID string) ([]*domain.ReviewEligibility, error)
}

type VendorOrderRepositoryPort interface {
	FindByID(ctx context.Context, id string) (*domain.VendorOrder, error)
	ListByVendor(ctx context.Context, vendorID, status string, limit, offset int) ([]*domain.VendorOrder, error)
	ListByOrderID(ctx context.Context, orderID string) ([]*domain.VendorOrder, error)
	TransitionStatus(ctx context.Context, id string, from, to domain.Status) error
	TransitionAllForOrder(ctx context.Context, orderID string, from, to domain.Status) (int64, error)
	SetLegacyCommission(ctx context.Context, id string, rule *domain.CommissionRule, commissionAmount, netAmount, base int64) error
	AddRefunded(ctx context.Context, id string, amount int64) (int64, error)
	SummaryByVendor(ctx context.Context, vendorID string) (*domain.VendorSummary, error)
	TopProductsByVendor(ctx context.Context, vendorID string, limit int) ([]*domain.TopProduct, error)
	QuantitySoldByProductIDs(ctx context.Context, productIDs []string) (map[string]int64, error)
	ListItemsByVendorOrderIDs(ctx context.Context, vendorOrderIDs []string) (map[string][]*domain.OrderItem, error)
	// HeldForSettlement names which vendor orders have an open return or
	// refund, with the reason.
	HeldForSettlement(ctx context.Context, vendorOrderIDs []string) (map[string]string, error)
}

// BuyerAddressRepositoryPort is a buyer's own saved shipping address book.
type BuyerAddressRepositoryPort interface {
	Create(ctx context.Context, a *domain.BuyerAddress) error
	FindByID(ctx context.Context, id string) (*domain.BuyerAddress, error)
	ListForBuyer(ctx context.Context, buyerID string) ([]*domain.BuyerAddress, error)
	FindDefaultForBuyer(ctx context.Context, buyerID string) (*domain.BuyerAddress, error)
	Update(ctx context.Context, id string, a *domain.BuyerAddress) error
	Delete(ctx context.Context, id string) error
	SetDefault(ctx context.Context, buyerID, addressID string) error
}

// CommissionRuleRepositoryPort is Order's insert-only commission ledger.
type CommissionRuleRepositoryPort interface {
	Create(ctx context.Context, rule *domain.CommissionRule) error
	FindCurrent(ctx context.Context) (*domain.CommissionRule, error)
	List(ctx context.Context, limit, offset int) ([]*domain.CommissionRule, error)
}

// CartConsumptionRepositoryPort persists the durable consume task written
// with each order (see domain.CartConsumption).
type CartConsumptionRepositoryPort interface {
	ListOpenByBuyer(ctx context.Context, buyerID string) ([]*domain.CartConsumption, error)
	Activate(ctx context.Context, orderID string) error
	Cancel(ctx context.Context, orderID string) error
	MarkConsumed(ctx context.Context, orderID string) error
	RecordFailure(ctx context.Context, orderID, reason string, park bool) error
	ClaimDue(ctx context.Context, limit int) ([]*domain.CartConsumption, error)
	ListStaleHeld(ctx context.Context, olderThan time.Time, limit int) ([]*domain.CartConsumption, error)
	Stats(ctx context.Context) (domain.CartConsumptionStats, error)
}

type CheckoutOperationRepositoryPort interface {
	Begin(ctx context.Context, buyerID, key, hash string, ttl time.Duration) (*domain.CheckoutOperation, bool, error)
	Complete(ctx context.Context, id, orderID string) error
	Fail(ctx context.Context, id string, cause *apperror.Error) error
	FindByID(ctx context.Context, id string) (*domain.CheckoutOperation, error)
	ListStalePreparing(ctx context.Context, olderThan time.Time, limit int) ([]*domain.CheckoutOperation, error)
	PurgeExpired(ctx context.Context, limit int) (int64, error)
}

type PaymentRecordRepositoryPort interface {
	Find(ctx context.Context, paymentID string) (*domain.OrderPayment, error)
	Insert(ctx context.Context, p *domain.OrderPayment) error
	ListByOrder(ctx context.Context, orderID string) ([]*domain.OrderPayment, error)
	ListRejected(ctx context.Context, limit, offset int) ([]*domain.OrderPayment, error)
}

type EffectRepositoryPort interface {
	Enqueue(ctx context.Context, effects ...domain.Effect) error
	ClaimDue(ctx context.Context, orderID string, limit int) ([]*domain.Effect, error)
	MarkDone(ctx context.Context, id string) error
	RecordFailure(ctx context.Context, id, reason string, park bool) (bool, error)
	Replay(ctx context.Context, id string) (bool, error)
	Find(ctx context.Context, id string) (*domain.Effect, error)
	ListByOrder(ctx context.Context, orderID string) ([]*domain.Effect, error)
	ListParked(ctx context.Context, limit, offset int) ([]*domain.Effect, error)
	Stats(ctx context.Context) (domain.EffectStats, error)
}

type RefundRepositoryPort interface {
	Create(ctx context.Context, f *domain.Refund) error
	FindByID(ctx context.Context, id string) (*domain.Refund, error)
	FindByIdempotencyKey(ctx context.Context, orderID, key string) (*domain.Refund, error)
	Transition(ctx context.Context, id string, from, to domain.RefundStatus, paymentRefundID, failure *string) error
	OpenTotals(ctx context.Context, orderID, vendorOrderID string) (int64, int64, error)
	ListByOrder(ctx context.Context, orderID string) ([]*domain.Refund, error)
	List(ctx context.Context, status string, limit, offset int) ([]*domain.Refund, error)
}

type ReturnRepositoryPort interface {
	Create(ctx context.Context, rr *domain.ReturnRequest) error
	ReturnedQuantity(ctx context.Context, itemID string) (int64, error)
	FindByID(ctx context.Context, id string) (*domain.ReturnRequest, error)
	VendorOf(ctx context.Context, id string) (vendorOrderID, vendorID string, err error)
	Transition(ctx context.Context, rr *domain.ReturnRequest, to domain.ReturnRequestStatus, u repository.ReturnUpdate) error
	AddEvent(ctx context.Context, e *domain.ReturnEvent) error
	ListEvents(ctx context.Context, returnID string) ([]*domain.ReturnEvent, error)
	ListByBuyer(ctx context.Context, buyerID string, limit, offset int) ([]*domain.ReturnRequest, error)
	ListByOrder(ctx context.Context, orderID string) ([]*domain.ReturnRequest, error)
	ListByStatus(ctx context.Context, status string, limit, offset int) ([]*domain.ReturnRequest, error)
	ListForVendor(ctx context.Context, vendorID, status string, limit, offset int) ([]*domain.ReturnRequest, error)
}

// SupportCaseRepositoryPort stores support cases with their immutable
// messages, timeline and evidence attachments.
type SupportCaseRepositoryPort interface {
	Create(ctx context.Context, c *domain.SupportCase) error
	FindByID(ctx context.Context, id string) (*domain.SupportCase, error)
	FindByIdempotencyKey(ctx context.Context, buyerID, key string) (*domain.SupportCase, error)
	FindNotClosed(ctx context.Context, buyerID, vendorOrderID string, category domain.SupportCategory) (*domain.SupportCase, error)
	Save(ctx context.Context, c *domain.SupportCase) error
	List(ctx context.Context, f repository.SupportCaseFilter, after *repository.CaseCursor, limit int) ([]*domain.SupportCase, error)
	ListPendingResolution(ctx context.Context, kind, ref string) ([]*domain.SupportCase, error)
	ListResolvedBefore(ctx context.Context, t time.Time, limit int) ([]*domain.SupportCase, error)
	AddMessage(ctx context.Context, m *domain.SupportMessage) error
	FindMessageByKey(ctx context.Context, authorID, key string) (*domain.SupportMessage, error)
	ListMessages(ctx context.Context, caseID string, includeInternal bool) ([]*domain.SupportMessage, error)
	AddEvent(ctx context.Context, e *domain.SupportCaseEvent) error
	ListEvents(ctx context.Context, caseID string) ([]*domain.SupportCaseEvent, error)
	CreateAttachment(ctx context.Context, a *domain.CaseAttachment) error
	FindAttachment(ctx context.Context, id string) (*domain.CaseAttachment, error)
	AttachToMessage(ctx context.Context, ids []string, ownerID, caseID, messageID string) (int64, error)
	ListOrphanAttachments(ctx context.Context, before time.Time, limit int) ([]*domain.CaseAttachment, error)
	ListExpiredAttachments(ctx context.Context, closedBefore time.Time, limit int) ([]*domain.CaseAttachment, error)
	MarkAttachmentDeleted(ctx context.Context, id, from string) (bool, error)
}

// AttachmentStore keeps support evidence in a private bucket; objects are
// read back only through Order after an access check.
type AttachmentStore interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// CartGateway is Cart's internal checkout contract: read the cart for a
// preview, freeze it for one checkout operation, then — once the order
// stands — consume only the purchased lines.
type CartGateway interface {
	Lines(ctx context.Context, buyerID string) (*adapter.CartSnapshot, error)
	Snapshot(ctx context.Context, buyerID, operationID string, expectedVersion *int64) (*adapter.CartSnapshot, error)
	Consume(ctx context.Context, buyerID, operationID string, lines []adapter.CartConsumeLine) error
}

// CatalogGateway is the checkout pricing snapshot's source of truth.
type CatalogGateway interface {
	GetCheckoutSnapshot(ctx context.Context, productIDs, variantIDs []string) (*adapter.CatalogSnapshot, error)
	GetProduct(ctx context.Context, productID string) (*adapter.ProductInfo, error)
	GetVariant(ctx context.Context, variantID string) (*adapter.VariantInfo, error)
}

// VendorGateway checks shop selling permission and vendor ownership.
type VendorGateway interface {
	Approved(context.Context, []string) (map[string]int64, error)
	GetApprovedVendorID(ctx context.Context, userID, vendorID string) (string, error)
}

// InventoryGateway is the reserve/release/commit contract, plus putting
// received returns back into stock.
type InventoryGateway interface {
	Operation(context.Context, string) (*adapter.ReservationReceipt, error)
	Reserve(ctx context.Context, orderID string, lines []adapter.ReserveLine) error
	Release(ctx context.Context, orderID string) error
	Commit(ctx context.Context, orderID string) error
	RestockReturn(ctx context.Context, returnID, productID string, variantID *string, quantity int64) error
}

// NotificationGateway sends a buyer notification; always called from a
// durable effect, never inline with a transition.
type NotificationGateway interface {
	Notify(ctx context.Context, eventID, userID, notifType, referenceID string) error
}

// ShipmentGateway quotes shipping before an order exists and opens/cancels
// shipments after payment/cancellation.
type ShipmentGateway interface {
	Quote(ctx context.Context, vendorID, province string, weightGrams int64) (*domain.ShippingQuote, error)
	CreateShipment(ctx context.Context, in adapter.CreateShipmentInput) (string, error)
	CancelForVendorOrder(ctx context.Context, vendorOrderID string) error
}

// PaymentGateway submits refunds; Payment owns the money movement.
type PaymentGateway interface {
	RequestRefund(ctx context.Context, r adapter.RefundRequest) (*adapter.RefundReceipt, error)
	SettleVendorOrder(ctx context.Context, r adapter.SettlementReport) error
}

// IdentityGateway re-verifies a sensitive actor's role with Identity
// instead of trusting the token claim alone.
type IdentityGateway interface {
	RequireRole(ctx context.Context, userID, role string) error
}

// AuditRepositoryPort appends admin actions in the caller's transaction.
type AuditRepositoryPort interface {
	Record(ctx context.Context, a domain.AdminAction) error
}

// OperationsReader counts work waiting for an operator.
type OperationsReader interface {
	Counts(ctx context.Context) (map[string]int64, error)
}

// TransactionRunner runs fn in one database transaction.
type TransactionRunner interface {
	Run(ctx context.Context, fn func(context.Context) error) error
}

// EventPublisher publishes a domain event (eventbus.Bus).
type EventPublisher interface {
	Publish(ctx context.Context, env eventbus.Envelope) error
}
