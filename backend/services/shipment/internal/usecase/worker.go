package usecase

import (
	"context"
	"time"

	"github.com/rs/zerolog"
)

// Worker logs fulfillment counters every minute (warn when something needs
// an operator) and removes buyer contact details from final shipments past
// the retention period every hour, with evidence uploads no report
// attached (PW-038).
type Worker struct {
	Shipments *ShipmentUseCase
	// Returns, when set, reports return parcels (AF-05).
	Returns *ReturnShipmentUseCase
	// Retention is how long a final shipment keeps the buyer's contact
	// details; zero disables redaction.
	Retention time.Duration
	Log       zerolog.Logger
}

func (w Worker) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	lastRedaction, lastClean := time.Time{}, time.Time{}
	for {
		w.report(ctx)
		w.backfillExceptions(ctx)
		if w.Returns != nil {
			w.Returns.Report(ctx)
		}
		if w.Retention > 0 && time.Since(lastRedaction) >= time.Hour {
			lastRedaction = time.Now()
			if n, err := w.Shipments.RedactAddresses(ctx, w.Retention); err != nil && ctx.Err() == nil {
				w.Log.Error().Err(err).Msg("shipment_address_redaction_failed")
			} else if n > 0 {
				w.Log.Info().Int64("shipments", n).Msg("shipment_addresses_redacted")
			}
		}
		if time.Since(lastClean) >= time.Hour {
			lastClean = time.Now()
			if n, err := w.Shipments.CleanEvidence(ctx); err != nil && ctx.Err() == nil {
				w.Log.Error().Err(err).Msg("shipment_evidence_cleanup_failed")
			} else if n > 0 {
				w.Log.Info().Int("files", n).Msg("shipment_evidence_orphans_removed")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// backfillExceptions (AF-04): packages returned before the feature get
// their exception fact once, in batches, so admins take them as cases.
func (w Worker) backfillExceptions(ctx context.Context) {
	if !w.Shipments.DeliveryResolution {
		return
	}
	n, err := w.Shipments.Outbox.EnqueueLegacyReturned(ctx, 200)
	if err != nil {
		if ctx.Err() == nil {
			w.Log.Error().Err(err).Msg("shipment_exception_backfill_failed")
		}
		return
	}
	if n > 0 {
		w.Log.Warn().Int64("shipments", n).Msg("shipment_exception_backfilled")
		w.Shipments.wake()
	}
}

func (w Worker) report(ctx context.Context) {
	counts, err := w.Shipments.Report(ctx)
	if err != nil {
		if ctx.Err() == nil {
			w.Log.Error().Err(err).Msg("shipment_report_failed")
		}
		return
	}
	attention := false
	for k, v := range counts {
		if v > 0 && k != "order_events_pending" {
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
	event.Msg("shipment_operations_report")
}
