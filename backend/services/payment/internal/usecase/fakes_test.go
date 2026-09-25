package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"shopee/backend/services/payment/internal/adapter"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/provider"
	"shopee/backend/services/payment/internal/repository"
)

type fakeIntentRepo struct {
	mu           sync.Mutex
	byID         map[string]*domain.PaymentIntent
	byProviderID map[string]string
	nextID       int
}

func newFakeIntentRepo() *fakeIntentRepo {
	return &fakeIntentRepo{byID: map[string]*domain.PaymentIntent{}, byProviderID: map[string]string{}}
}

func (f *fakeIntentRepo) Create(_ context.Context, intent *domain.PaymentIntent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	intent.ID = fmt.Sprintf("intent-%d", f.nextID)
	cp := *intent
	f.byID[intent.ID] = &cp
	f.byProviderID[intent.ProviderIntentID] = intent.ID
	return nil
}

func (f *fakeIntentRepo) FindByID(_ context.Context, id string) (*domain.PaymentIntent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, ok := f.byID[id]
	if !ok {
		return nil, repository.ErrPaymentIntentNotFound
	}
	cp := *i
	return &cp, nil
}

func (f *fakeIntentRepo) FindByProviderIntentID(_ context.Context, providerIntentID string) (*domain.PaymentIntent, error) {
	f.mu.Lock()
	id, ok := f.byProviderID[providerIntentID]
	f.mu.Unlock()
	if !ok {
		return nil, repository.ErrPaymentIntentNotFound
	}
	return f.FindByID(context.Background(), id)
}

func (f *fakeIntentRepo) FindPendingByOrderID(_ context.Context, orderID string) (*domain.PaymentIntent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, i := range f.byID {
		if i.OrderID == orderID && i.Status == domain.StatusPending {
			cp := *i
			return &cp, nil
		}
	}
	return nil, repository.ErrPaymentIntentNotFound
}

func (f *fakeIntentRepo) UpdateStatus(_ context.Context, id string, status domain.Status, failureReason *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, ok := f.byID[id]
	if !ok {
		return repository.ErrPaymentIntentNotFound
	}
	i.Status = status
	i.FailureReason = failureReason
	return nil
}

type fakeEventRepo struct {
	mu   sync.Mutex
	seen map[string]bool
}

func newFakeEventRepo() *fakeEventRepo {
	return &fakeEventRepo{seen: map[string]bool{}}
}

func (f *fakeEventRepo) RecordIfNew(_ context.Context, providerEventID, _, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen[providerEventID] {
		return false, nil
	}
	f.seen[providerEventID] = true
	return true, nil
}

type fakeOrderGateway struct {
	mu          sync.Mutex
	orders      map[string]*adapter.OrderSnapshot
	paidCalls   []string
	failedCalls []string
}

func newFakeOrderGateway() *fakeOrderGateway {
	return &fakeOrderGateway{orders: map[string]*adapter.OrderSnapshot{}}
}

func (f *fakeOrderGateway) GetOrder(_ context.Context, orderID string) (*adapter.OrderSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.orders[orderID]
	if !ok {
		return nil, errors.New("order not found")
	}
	cp := *o
	return &cp, nil
}

func (f *fakeOrderGateway) MarkPaid(_ context.Context, orderID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paidCalls = append(f.paidCalls, orderID)
	return nil
}

func (f *fakeOrderGateway) MarkPaymentFailed(_ context.Context, orderID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failedCalls = append(f.failedCalls, orderID)
	return nil
}

// fakeProvider issues predictable, incrementing provider intent ids.
type fakeProvider struct {
	mu     sync.Mutex
	nextID int
}

func (f *fakeProvider) CreateIntent(_ context.Context, _ provider.CreateIntentInput) (provider.CreateIntentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	return provider.CreateIntentResult{ProviderIntentID: fmt.Sprintf("pi-%d", f.nextID)}, nil
}

// fakeVerifier treats the payload as a plain JSON-encoded provider.WebhookEvent
// and only "verifies" a fixed sentinel signature, so tests can construct
// events directly without depending on the mock package's HMAC scheme
// (which has its own dedicated tests).
const fakeValidSignature = "valid"

type fakeVerifier struct{}

func (fakeVerifier) Verify(payload []byte, signatureHeader string) (provider.WebhookEvent, error) {
	if signatureHeader != fakeValidSignature {
		return provider.WebhookEvent{}, errors.New("invalid signature")
	}
	var evt provider.WebhookEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		return provider.WebhookEvent{}, err
	}
	return evt, nil
}

func mustMarshalEvent(evt provider.WebhookEvent) []byte {
	b, err := json.Marshal(evt)
	if err != nil {
		panic(err)
	}
	return b
}
