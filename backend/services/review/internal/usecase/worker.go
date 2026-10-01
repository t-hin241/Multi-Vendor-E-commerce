package usecase

import (
	"context"
	"time"
)

// Maintenance runs Review's background work every minute: removing the
// objects of failed image uploads and resolving author labels of reviews
// written before labels were stored.
func (u *ReviewUseCase) Maintenance(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if _, err := u.CleanUploads(ctx); err != nil && ctx.Err() == nil {
			u.Log.Error().Err(err).Msg("review_image_cleanup_round_failed")
		}
		if _, err := u.BackfillLabels(ctx, 100); err != nil && ctx.Err() == nil {
			u.Log.Error().Err(err).Msg("review_author_label_backfill_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
