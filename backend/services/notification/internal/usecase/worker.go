package usecase

import (
	"context"
	"time"
)

// Maintenance runs next to the job workers: every 30 seconds it queues a
// job again for any notification that is overdue in PostgreSQL (Recover),
// every minute it logs the delivery report (warn when something is parked,
// late or the queue is unreachable), and once an hour it trims old attempt
// history. While delivery is paused (NOTIFICATION_DELIVERY_PAUSED) no job
// runs and nothing is requeued; requests keep being recorded and queued.
type Maintenance struct {
	UseCase *NotificationUseCase
	// Retention of attempt rows; zero keeps them.
	AttemptRetention time.Duration
	Paused           bool
}

func (m *Maintenance) Run(ctx context.Context) {
	recoverTick := time.NewTicker(30 * time.Second)
	report := time.NewTicker(time.Minute)
	purge := time.NewTicker(time.Hour)
	defer recoverTick.Stop()
	defer report.Stop()
	defer purge.Stop()
	log := m.UseCase.Log
	if m.Paused {
		log.Warn().Msg("notification_delivery_paused")
	} else {
		m.recover(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-recoverTick.C:
			if !m.Paused {
				m.recover(ctx)
			}
		case <-report.C:
			m.report(ctx)
		case <-purge.C:
			if m.AttemptRetention > 0 {
				if n, err := m.UseCase.PurgeAttempts(ctx, m.AttemptRetention); err != nil {
					log.Error().Err(err).Msg("notification_attempts_purge_failed")
				} else if n > 0 {
					log.Info().Int64("rows", n).Msg("notification_attempts_purged")
				}
			}
		}
	}
}

func (m *Maintenance) recover(ctx context.Context) {
	if _, err := m.UseCase.Recover(ctx); err != nil {
		m.UseCase.Log.Error().Err(err).Msg("notification_recover_failed")
	}
}

func (m *Maintenance) report(ctx context.Context) {
	counts, err := m.UseCase.Report(ctx)
	if err != nil {
		m.UseCase.Log.Error().Err(err).Msg("notification_report_failed")
		return
	}
	event := m.UseCase.Log.Info()
	if counts["parked"] > 0 || counts["pending_over_15m"] > 0 || counts["queue_unreachable"] > 0 {
		event = m.UseCase.Log.Warn()
	}
	for k, v := range counts {
		event = event.Int64(k, v)
	}
	event.Bool("paused", m.Paused).Msg("notification_delivery_report")
}
