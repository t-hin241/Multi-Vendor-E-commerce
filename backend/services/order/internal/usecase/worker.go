package usecase

import (
	"context"
	"time"

	"github.com/rs/zerolog"
)

// OrderWorker drives Order's durable workflows: cart-consume tasks, side
// effects of transitions (shipments, stock release, notifications, refunds,
// return restock), recovery of checkouts whose request died, closing
// support cases past their reopen window and cleaning their evidence. It also
// reports the backlogs once a minute for log-based alerting.
type OrderWorker struct {
	UseCase  *OrderUseCase
	Interval time.Duration
	Batch    int
}

func (w OrderWorker) Run(ctx context.Context) {
	interval, batch := w.Interval, w.Batch
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if batch <= 0 {
		batch = 20
	}
	uc := w.UseCase
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastReport := time.Time{}
	for {
		if _, err := uc.ProcessCartConsumptions(ctx, batch); err != nil && ctx.Err() == nil {
			uc.Log.Error().Err(err).Msg("order_cart_consume_worker_failed")
		}
		// Effects are claimed with a lease (a failed one is rescheduled), so
		// draining never spins on the same rows.
		drain(ctx, interval, batch, func() (int, error) { return uc.ProcessEffects(ctx, "", batch) }, func(err error) {
			uc.Log.Error().Err(err).Msg("order_effect_worker_failed")
		})
		if _, err := uc.RecoverCheckouts(ctx, batch); err != nil && ctx.Err() == nil {
			uc.Log.Error().Err(err).Msg("order_checkout_recovery_worker_failed")
		}
		if time.Since(lastReport) >= time.Minute {
			lastReport = time.Now()
			w.report(ctx)
			if _, err := uc.CheckoutOps.PurgeExpired(ctx, 500); err != nil && ctx.Err() == nil {
				uc.Log.Error().Err(err).Msg("order_checkout_purge_failed")
			}
			if _, err := uc.CloseExpiredSupportCases(ctx, batch); err != nil && ctx.Err() == nil {
				uc.Log.Error().Err(err).Msg("order_support_auto_close_failed")
			}
			if _, err := uc.CleanSupportAttachments(ctx, batch); err != nil && ctx.Err() == nil {
				uc.Log.Error().Err(err).Msg("order_support_attachment_cleanup_failed")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// drain runs batches while they come back full, for at most budget, so a
// backlog empties at the speed the work runs instead of one batch per tick
// (20 effects per 5s was the ceiling of the paid-order pipeline under load:
// docs/module-details/17-observability-capacity.md).
func drain(ctx context.Context, budget time.Duration, batch int, run func() (int, error), failed func(error)) {
	deadline := time.Now().Add(budget)
	for ctx.Err() == nil {
		n, err := run()
		if err != nil {
			if ctx.Err() == nil {
				failed(err)
			}
			return
		}
		if n < batch || time.Now().After(deadline) {
			return
		}
	}
}

func (w OrderWorker) report(ctx context.Context) {
	uc := w.UseCase
	if stats, err := uc.CartConsumption.Stats(ctx); err == nil {
		if stats.Held > 0 || stats.Pending > 0 || stats.Parked > 0 {
			event := levelFor(uc.Log, stats.Parked > 0 || (stats.OldestPending != nil && time.Since(*stats.OldestPending) > 5*time.Minute))
			if stats.OldestPending != nil {
				event = event.Int64("oldest_pending_seconds", int64(time.Since(*stats.OldestPending).Seconds()))
			}
			event.Int64("held", stats.Held).Int64("pending", stats.Pending).Int64("parked", stats.Parked).Msg("order_cart_consume_backlog")
		}
	} else if ctx.Err() == nil {
		uc.Log.Error().Err(err).Msg("order_cart_consume_stats_failed")
	}
	if stats, err := uc.Effects.Stats(ctx); err == nil {
		if stats.Pending > 0 || stats.Parked > 0 {
			event := levelFor(uc.Log, stats.Parked > 0 || (stats.OldestPending != nil && time.Since(*stats.OldestPending) > 5*time.Minute))
			if stats.OldestPending != nil {
				event = event.Int64("oldest_pending_seconds", int64(time.Since(*stats.OldestPending).Seconds()))
			}
			event.Int64("pending", stats.Pending).Int64("parked", stats.Parked).Msg("order_effect_backlog")
		}
	} else if ctx.Err() == nil {
		uc.Log.Error().Err(err).Msg("order_effect_stats_failed")
	}
	if exceptions, err := uc.Payments.ListRejected(ctx, 1, 0); err == nil && len(exceptions) > 0 {
		uc.Log.Warn().Str("oldest_payment_id", exceptions[0].PaymentID).Msg("order_payment_exceptions_pending")
	}
}

func levelFor(log zerolog.Logger, warn bool) *zerolog.Event {
	if warn {
		return log.Warn()
	}
	return log.Info()
}
