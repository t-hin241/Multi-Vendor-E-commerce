package usecase_test

import (
	"context"
	"errors"
	"testing"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/inventory/internal/repository"
	"shopee/backend/services/inventory/internal/usecase"

	"github.com/rs/zerolog"
)

type alertStore struct{ finished, success, parked bool }

func (s *alertStore) ClaimAlert(context.Context) (repository.StockAlert, error) {
	return repository.StockAlert{ID: "episode", Attempt: 1}, nil
}
func (s *alertStore) FinishAlert(_ context.Context, _ repository.StockAlert, ok bool) error {
	s.finished = true
	s.success = ok
	return nil
}
func (s *alertStore) ParkAlert(context.Context, repository.StockAlert) error {
	s.parked = true
	return nil
}

type alertSender struct{ err error }

func (s alertSender) Send(context.Context, repository.StockAlert) error { return s.err }
func TestStockAlertWorkerRetriesTransientAndParksRefusals(t *testing.T) {
	for _, tc := range []struct {
		name          string
		err           error
		park, success bool
	}{
		{"sent", nil, false, true}, {"timeout", errors.New("fake timeout"), false, false},
		{"refused", &apperror.Error{Status: 403}, true, false}, {"rate limited", &apperror.Error{Status: 429}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &alertStore{}
			w := usecase.StockAlertWorker{Store: s, Notify: alertSender{tc.err}, Log: zerolog.Nop()}
			if err := w.Once(t.Context()); err != nil || s.parked != tc.park || s.success != tc.success || (!tc.park && !s.finished) {
				t.Fatalf("result %+v %v", s, err)
			}
		})
	}
}
