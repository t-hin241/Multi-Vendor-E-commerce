package usecase_test

import (
	"testing"

	"github.com/rs/zerolog"

	"shopee/backend/services/notification/internal/adapter"
	"shopee/backend/services/notification/internal/domain"
	"shopee/backend/services/notification/internal/usecase"
)

func TestNotify_RecordsSentOnSuccess(t *testing.T) {
	repo := newFakeNotificationRepo()
	identity := newFakeIdentityGateway()
	identity.users["user-1"] = &adapter.UserSnapshot{ID: "user-1", Email: "buyer@example.com", FullName: "Buyer One"}
	sndr := &fakeSender{}
	uc := usecase.NewNotificationUseCase(repo, identity, sndr, zerolog.Nop())

	if err := uc.Notify(t.Context(), "user-1", domain.TypeOrderPaid, "order-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sndr.sent) != 1 {
		t.Fatalf("expected one email sent, got %d", len(sndr.sent))
	}
	if len(repo.records) != 1 || repo.records[0].Status != domain.StatusSent {
		t.Errorf("expected a sent record, got %+v", repo.records)
	}
}

func TestNotify_RecordsFailedWhenUserNotFound(t *testing.T) {
	repo := newFakeNotificationRepo()
	identity := newFakeIdentityGateway() // no users registered
	sndr := &fakeSender{}
	uc := usecase.NewNotificationUseCase(repo, identity, sndr, zerolog.Nop())

	err := uc.Notify(t.Context(), "missing-user", domain.TypeOrderPaid, "order-1")
	if err == nil {
		t.Fatal("expected an error when the recipient can't be resolved")
	}
	if len(repo.records) != 1 || repo.records[0].Status != domain.StatusFailed {
		t.Errorf("expected a failed record, got %+v", repo.records)
	}
	if len(sndr.sent) != 0 {
		t.Error("expected no email to be sent when the recipient can't be resolved")
	}
}

func TestNotify_RecordsFailedWhenSendFails(t *testing.T) {
	repo := newFakeNotificationRepo()
	identity := newFakeIdentityGateway()
	identity.users["user-1"] = &adapter.UserSnapshot{ID: "user-1", Email: "buyer@example.com", FullName: "Buyer One"}
	sndr := &fakeSender{failNext: true}
	uc := usecase.NewNotificationUseCase(repo, identity, sndr, zerolog.Nop())

	err := uc.Notify(t.Context(), "user-1", domain.TypeOrderShipped, "order-1")
	if err == nil {
		t.Fatal("expected an error when sending fails")
	}
	if len(repo.records) != 1 || repo.records[0].Status != domain.StatusFailed {
		t.Errorf("expected a failed record, got %+v", repo.records)
	}
}
