package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"shopee/backend/services/notification/internal/adapter"
	"shopee/backend/services/notification/internal/domain"
	"shopee/backend/services/notification/internal/sender"
)

type fakeNotificationRepo struct {
	mu      sync.Mutex
	records []*domain.Notification
	nextID  int
}

func newFakeNotificationRepo() *fakeNotificationRepo {
	return &fakeNotificationRepo{}
}

func (f *fakeNotificationRepo) Create(_ context.Context, n *domain.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	n.ID = fmt.Sprintf("notif-%d", f.nextID)
	cp := *n
	f.records = append(f.records, &cp)
	return nil
}

func (f *fakeNotificationRepo) List(_ context.Context, _, _ int) ([]*domain.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.records, nil
}

type fakeIdentityGateway struct {
	users map[string]*adapter.UserSnapshot
}

func newFakeIdentityGateway() *fakeIdentityGateway {
	return &fakeIdentityGateway{users: map[string]*adapter.UserSnapshot{}}
}

func (f *fakeIdentityGateway) GetUser(_ context.Context, userID string) (*adapter.UserSnapshot, error) {
	u, ok := f.users[userID]
	if !ok {
		return nil, errors.New("user not found")
	}
	cp := *u
	return &cp, nil
}

type fakeSender struct {
	mu       sync.Mutex
	sent     []sender.Email
	failNext bool
}

func (f *fakeSender) Send(_ context.Context, email sender.Email) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext {
		f.failNext = false
		return errors.New("simulated send failure")
	}
	f.sent = append(f.sent, email)
	return nil
}
