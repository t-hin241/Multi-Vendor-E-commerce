package usecase_test

import (
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/adapter"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/provider"
	"shopee/backend/services/payment/internal/usecase"
)

func mustAppError(t *testing.T, err error) *apperror.Error {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *apperror.Error, got %T: %v", err, err)
	}
	return appErr
}

type fixture struct {
	uc      *usecase.PaymentUseCase
	intents *fakeIntentRepo
	events  *fakeEventRepo
	orders  *fakeOrderGateway
	prov    *fakeProvider
}

func newFixture() *fixture {
	intents := newFakeIntentRepo()
	events := newFakeEventRepo()
	orders := newFakeOrderGateway()
	prov := &fakeProvider{}

	uc := usecase.NewPaymentUseCase(intents, events, orders, prov, fakeVerifier{}, nil, "mock", zerolog.Nop())
	return &fixture{uc: uc, intents: intents, events: events, orders: orders, prov: prov}
}

func TestCreateIntent_SizesFromOrderNotClient(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.orders.orders["order-1"] = &adapter.OrderSnapshot{ID: "order-1", BuyerID: "buyer-1", Status: "pending_payment", TotalAmount: 5000, Currency: "VND"}

	intent, err := f.uc.CreateIntent(ctx, "buyer-1", "order-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if intent.Amount != 5000 || intent.Currency != "VND" {
		t.Errorf("expected intent sized from the order (5000 VND), got %d %s", intent.Amount, intent.Currency)
	}
	if intent.Status != domain.StatusPending {
		t.Errorf("expected pending, got %q", intent.Status)
	}
}

func TestCreateIntent_ReusesExistingPendingIntent(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.orders.orders["order-1"] = &adapter.OrderSnapshot{ID: "order-1", BuyerID: "buyer-1", Status: "pending_payment", TotalAmount: 5000, Currency: "VND"}

	first, err := f.uc.CreateIntent(ctx, "buyer-1", "order-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := f.uc.CreateIntent(ctx, "buyer-1", "order-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("expected the second call to reuse the same pending intent, got %s and %s", first.ID, second.ID)
	}
}

func TestCreateIntent_RejectsNonOwner(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.orders.orders["order-1"] = &adapter.OrderSnapshot{ID: "order-1", BuyerID: "buyer-1", Status: "pending_payment", TotalAmount: 5000, Currency: "VND"}

	_, err := f.uc.CreateIntent(ctx, "someone-else", "order-1")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden, got %v", appErr.Code)
	}
}

func TestCreateIntent_RejectsOrderNotAwaitingPayment(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.orders.orders["order-1"] = &adapter.OrderSnapshot{ID: "order-1", BuyerID: "buyer-1", Status: "paid", TotalAmount: 5000, Currency: "VND"}

	_, err := f.uc.CreateIntent(ctx, "buyer-1", "order-1")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict, got %v", appErr.Code)
	}
}

func TestProcessWebhook_SucceededMarksOrderPaid(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.orders.orders["order-1"] = &adapter.OrderSnapshot{ID: "order-1", BuyerID: "buyer-1", Status: "pending_payment", TotalAmount: 5000, Currency: "VND"}
	intent, err := f.uc.CreateIntent(ctx, "buyer-1", "order-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	payload := mustMarshalEvent(provider.WebhookEvent{
		ProviderEventID: "evt-1", ProviderIntentID: intent.ProviderIntentID,
		Type: provider.EventPaymentSucceeded, Amount: 5000, Currency: "VND",
	})
	if err := f.uc.ProcessWebhook(ctx, payload, fakeValidSignature); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated, err := f.intents.FindByID(ctx, intent.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.Status != domain.StatusCaptured {
		t.Errorf("expected captured, got %q", updated.Status)
	}
	if len(f.orders.paidCalls) != 1 || f.orders.paidCalls[0] != "order-1" {
		t.Errorf("expected Order.MarkPaid to be called once for order-1, got %v", f.orders.paidCalls)
	}
}

func TestProcessWebhook_DuplicateDeliveryIsIgnored(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.orders.orders["order-1"] = &adapter.OrderSnapshot{ID: "order-1", BuyerID: "buyer-1", Status: "pending_payment", TotalAmount: 5000, Currency: "VND"}
	intent, err := f.uc.CreateIntent(ctx, "buyer-1", "order-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	payload := mustMarshalEvent(provider.WebhookEvent{
		ProviderEventID: "evt-1", ProviderIntentID: intent.ProviderIntentID,
		Type: provider.EventPaymentSucceeded, Amount: 5000, Currency: "VND",
	})

	if err := f.uc.ProcessWebhook(ctx, payload, fakeValidSignature); err != nil {
		t.Fatalf("unexpected error on first delivery: %v", err)
	}
	if err := f.uc.ProcessWebhook(ctx, payload, fakeValidSignature); err != nil {
		t.Fatalf("unexpected error on retried delivery: %v", err)
	}

	if len(f.orders.paidCalls) != 1 {
		t.Errorf("expected Order.MarkPaid to be called exactly once despite a retried webhook, got %d calls", len(f.orders.paidCalls))
	}
}

func TestProcessWebhook_RejectsInvalidSignature(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.orders.orders["order-1"] = &adapter.OrderSnapshot{ID: "order-1", BuyerID: "buyer-1", Status: "pending_payment", TotalAmount: 5000, Currency: "VND"}
	intent, err := f.uc.CreateIntent(ctx, "buyer-1", "order-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	payload := mustMarshalEvent(provider.WebhookEvent{
		ProviderEventID: "evt-1", ProviderIntentID: intent.ProviderIntentID,
		Type: provider.EventPaymentSucceeded, Amount: 5000, Currency: "VND",
	})

	err = f.uc.ProcessWebhook(ctx, payload, "not-the-real-signature")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeUnauthorized {
		t.Errorf("expected unauthorized for a bad signature, got %v", appErr.Code)
	}
	if len(f.orders.paidCalls) != 0 {
		t.Error("expected Order.MarkPaid never to be called for an unverified webhook")
	}
}

func TestProcessWebhook_AmountMismatchRefusesToMarkOrderPaid(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.orders.orders["order-1"] = &adapter.OrderSnapshot{ID: "order-1", BuyerID: "buyer-1", Status: "pending_payment", TotalAmount: 5000, Currency: "VND"}
	intent, err := f.uc.CreateIntent(ctx, "buyer-1", "order-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The provider claims a different (lower) amount was collected than the
	// intent was created for — must never be trusted.
	payload := mustMarshalEvent(provider.WebhookEvent{
		ProviderEventID: "evt-1", ProviderIntentID: intent.ProviderIntentID,
		Type: provider.EventPaymentSucceeded, Amount: 1, Currency: "VND",
	})

	err = f.uc.ProcessWebhook(ctx, payload, fakeValidSignature)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict for an amount mismatch, got %v", appErr.Code)
	}
	if len(f.orders.paidCalls) != 0 {
		t.Error("expected Order.MarkPaid never to be called when amounts don't match")
	}
}

func TestProcessWebhook_FailedCancelsOrder(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.orders.orders["order-1"] = &adapter.OrderSnapshot{ID: "order-1", BuyerID: "buyer-1", Status: "pending_payment", TotalAmount: 5000, Currency: "VND"}
	intent, err := f.uc.CreateIntent(ctx, "buyer-1", "order-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	payload := mustMarshalEvent(provider.WebhookEvent{
		ProviderEventID: "evt-1", ProviderIntentID: intent.ProviderIntentID,
		Type: provider.EventPaymentFailed, FailureReason: "insufficient_funds",
	})
	if err := f.uc.ProcessWebhook(ctx, payload, fakeValidSignature); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated, err := f.intents.FindByID(ctx, intent.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.Status != domain.StatusFailed {
		t.Errorf("expected failed, got %q", updated.Status)
	}
	if len(f.orders.failedCalls) != 1 || f.orders.failedCalls[0] != "order-1" {
		t.Errorf("expected Order.MarkPaymentFailed to be called once for order-1, got %v", f.orders.failedCalls)
	}
}

func TestSimulate_UnavailableWithoutASimulator(t *testing.T) {
	f := newFixture() // simulator is nil, as a real (non-mock) provider deployment would leave it
	ctx := t.Context()
	f.orders.orders["order-1"] = &adapter.OrderSnapshot{ID: "order-1", BuyerID: "buyer-1", Status: "pending_payment", TotalAmount: 5000, Currency: "VND"}
	intent, err := f.uc.CreateIntent(ctx, "buyer-1", "order-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = f.uc.Simulate(ctx, "buyer-1", intent.ID, true, "")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error when no simulator is wired, got %v", appErr.Code)
	}
}
