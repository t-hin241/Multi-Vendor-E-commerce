package usecase

import (
	"context"
	"time"

	"github.com/rs/zerolog"
)

// PaymentWorker re-applies unfinished receipts and reconciles stale
// provider links every 30 seconds, and logs the reconciliation counters
// every minute (warn when something needs an operator).
type PaymentWorker struct {
	Payments       *PaymentUseCase
	Reconciliation *ReconciliationUseCase
	Log            zerolog.Logger
}

func (w PaymentWorker) Run(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	lastReport := time.Time{}
	for {
		if err := w.Payments.Reconcile(ctx, 20); err != nil && ctx.Err() == nil {
			w.Log.Error().Err(err).Msg("payment_reconcile_failed")
		}
		if time.Since(lastReport) >= time.Minute {
			lastReport = time.Now()
			w.report(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (w PaymentWorker) report(ctx context.Context) {
	counts, err := w.Reconciliation.Report(ctx)
	if err != nil {
		if ctx.Err() == nil {
			w.Log.Error().Err(err).Msg("payment_reconciliation_report_failed")
		}
		return
	}
	attention := false
	for _, key := range []string{"receipts_parked", "receipts_rejected", "receipts_unapplied", "intents_stuck_creating", "intents_expired_open", "order_sync_review"} {
		if counts[key] > 0 {
			attention = true
		}
	}
	event := w.Log.Info()
	if attention {
		event = w.Log.Warn()
	}
	for k, v := range counts {
		event = event.Int64(k, v)
	}
	event.Msg("payment_reconciliation_report")
}
