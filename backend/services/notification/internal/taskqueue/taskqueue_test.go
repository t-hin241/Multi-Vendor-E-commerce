package taskqueue

import (
	"context"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
)

func TestHandlerRunsTheAttemptNamedByTheJob(t *testing.T) {
	var gotID string
	var gotAttempt int
	h := Handler(func(_ context.Context, id string, attempt int) error {
		gotID, gotAttempt = id, attempt
		return nil
	})
	if err := h.ProcessTask(t.Context(), asynq.NewTask(TypeDeliver, []byte(`{"notification_id":"n-1","attempt":3}`))); err != nil {
		t.Fatal(err)
	}
	if gotID != "n-1" || gotAttempt != 3 {
		t.Fatalf("got %q attempt %d", gotID, gotAttempt)
	}
}

func TestMalformedJobIsNotRetried(t *testing.T) {
	h := Handler(func(context.Context, string, int) error {
		t.Fatal("a malformed job must not be delivered")
		return nil
	})
	for _, payload := range []string{`not json`, `{"notification_id":"","attempt":1}`, `{"notification_id":"n-1","attempt":0}`} {
		err := h.ProcessTask(t.Context(), asynq.NewTask(TypeDeliver, []byte(payload)))
		if !errors.Is(err, asynq.SkipRetry) {
			t.Fatalf("%s: expected SkipRetry, got %v", payload, err)
		}
	}
}

func TestDeliveryErrorIsRetriedByTheQueue(t *testing.T) {
	boom := errors.New("database unavailable")
	h := Handler(func(context.Context, string, int) error { return boom })
	err := h.ProcessTask(t.Context(), asynq.NewTask(TypeDeliver, []byte(`{"notification_id":"n-1","attempt":1}`)))
	if !errors.Is(err, boom) || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("expected a retryable error, got %v", err)
	}
}
