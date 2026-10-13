package usecase

import (
	"context"
	"errors"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/inventory/internal/repository"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
)

type StockAlertStore interface {
	ClaimAlert(context.Context) (repository.StockAlert, error)
	FinishAlert(context.Context, repository.StockAlert, bool) error
	ParkAlert(context.Context, repository.StockAlert) error
}
type StockAlertNotifier interface {
	Send(context.Context, repository.StockAlert) error
}
type StockAlertWorker struct {
	Store  StockAlertStore
	Notify StockAlertNotifier
	Log    zerolog.Logger
}

func (w StockAlertWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for range 50 {
				c, cancel := context.WithTimeout(ctx, 10*time.Second)
				err := w.Once(c)
				cancel()
				if errors.Is(err, pgx.ErrNoRows) {
					break
				}
				if err != nil {
					w.Log.Error().Err(err).Msg("stock_alert_delivery_failed")
					break
				}
			}
		}
	}
}
func (w StockAlertWorker) Once(ctx context.Context) error {
	a, err := w.Store.ClaimAlert(ctx)
	if err != nil {
		return err
	}
	err = w.Notify.Send(ctx, a)
	if err != nil {
		w.Log.Warn().Str("episode_id", a.ID).Int("attempt", a.Attempt).Msg("stock_alert_retry_or_review")
		var app *apperror.Error
		if errors.As(err, &app) && app.Status >= 400 && app.Status < 500 && app.Status != 429 {
			return w.Store.ParkAlert(ctx, a)
		}
	}
	return w.Store.FinishAlert(ctx, a, err == nil)
}
