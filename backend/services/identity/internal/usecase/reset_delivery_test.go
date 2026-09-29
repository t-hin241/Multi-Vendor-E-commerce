package usecase_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/services/identity/internal/repository"
	"shopee/backend/services/identity/internal/usecase"
)

type deliveryStore struct {
	record   *repository.ResetDelivery
	success  bool
	finished bool
}

func (s *deliveryStore) ClaimDelivery(context.Context) (string, error) { return s.record.ID, nil }
func (s *deliveryStore) ReadDelivery(context.Context, string) (*repository.ResetDelivery, error) {
	return s.record, nil
}
func (s *deliveryStore) FinishDelivery(_ context.Context, _ string, success bool) error {
	s.success = success
	s.finished = true
	return nil
}

type notifier struct {
	id  string
	err error
}

func (n *notifier) NotifyReset(_ context.Context, id string) error { n.id = id; return n.err }
func TestDeliveryUsesReferenceAndRecordsRetry(t *testing.T) {
	cipher, err := usecase.NewTokenCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt("synthetic-reset", "user")
	if err != nil {
		t.Fatal(err)
	}
	store := &deliveryStore{record: &repository.ResetDelivery{ID: "delivery-id", UserID: "user", Email: "buyer@example.invalid", EncryptedToken: encrypted}}
	notify := &notifier{err: errors.New("offline")}
	log := &bytes.Buffer{}
	uc := &usecase.ResetDeliveryUseCase{Store: store, Cipher: cipher, Notifier: notify, ResetURL: "https://shop.example.invalid/reset-password", Log: zerolog.New(log)}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	uc.DeliverNext(ctx)
	if notify.id != "delivery-id" || !store.finished || store.success {
		t.Fatal("failed send not recorded for retry")
	}
	message, err := uc.Message(ctx, "delivery-id")
	if err != nil || !strings.Contains(message.URL, "#token=") {
		t.Fatal("reset link must use fragment")
	}
	if strings.Contains(log.String(), "synthetic-reset") || strings.Contains(log.String(), "buyer@example.invalid") {
		t.Fatal("sensitive delivery material logged")
	}
	notify.err = nil
	uc.DeliverNext(ctx)
	if !store.success {
		t.Fatal("successful send not recorded")
	}
}
