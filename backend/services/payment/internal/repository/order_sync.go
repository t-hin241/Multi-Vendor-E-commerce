package repository

import (
	"context"
	"errors"
	"shopee/backend/pkg/apperror"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

type OrderSync struct{ Pool *pgxpool.Pool }

var ErrNoOrderSync = errors.New("no payment outcome to sync")

func (s OrderSync) Dispatch(ctx context.Context, deliver func(context.Context, string, string) error) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		if e := tx.Rollback(cleanup); e != nil && !errors.Is(e, pgx.ErrTxClosed) {
			err = errors.Join(err, e)
		}
	}()
	var id, order, outcome string
	err = tx.QueryRow(ctx, `SELECT s.payment_intent_id,p.order_id,s.outcome FROM payment_order_sync s JOIN payment_intents p ON p.id=s.payment_intent_id WHERE s.delivered_at IS NULL AND NOT s.requires_review AND s.next_attempt_at<=now() AND s.attempts<10 ORDER BY s.next_attempt_at,s.payment_intent_id LIMIT 1 FOR UPDATE OF s SKIP LOCKED`).Scan(&id, &order, &outcome)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoOrderSync
	}
	if err != nil {
		return err
	}
	deliveryErr := deliver(ctx, order, outcome)
	if deliveryErr == nil {
		_, err = tx.Exec(ctx, `UPDATE payment_order_sync SET delivered_at=now(),last_error=NULL WHERE payment_intent_id=$1`, id)
	} else {
		var app *apperror.Error
		review := errors.As(deliveryErr, &app) && app.Code == apperror.CodeConflict
		_, err = tx.Exec(ctx, `UPDATE payment_order_sync SET attempts=attempts+1,requires_review=$2 OR attempts>=9,last_error=CASE WHEN $2 THEN 'order_rejected_outcome' ELSE 'order_delivery_failed' END,next_attempt_at=now()+interval '1 second'*least(300,power(2,attempts)) WHERE payment_intent_id=$1`, id, review)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s OrderSync) Run(ctx context.Context, deliver func(context.Context, string, string) error, log zerolog.Logger) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		for i := 0; i < 20 && ctx.Err() == nil; i++ {
			err := s.Dispatch(ctx, deliver)
			if errors.Is(err, ErrNoOrderSync) {
				break
			}
			if err != nil {
				log.Error().Err(err).Msg("payment_order_sync_failed")
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
