package usecase

import (
	"context"
	"time"

	"github.com/rs/zerolog"
)

// RetentionPolicy controls the background cleanup of idle carts and old
// checkout operations. It never touches a cart that still has an
// unconsumed checkout operation.
type RetentionPolicy struct {
	Enabled      bool
	CartIdle     time.Duration
	OperationTTL time.Duration
	Interval     time.Duration
	BatchSize    int
	// MaxBatches bounds the work of a single tick.
	MaxBatches int
}

type RetentionWorker struct {
	repo   RetentionRepositoryPort
	policy RetentionPolicy
	log    zerolog.Logger
	now    func() time.Time
}

func NewRetentionWorker(repo RetentionRepositoryPort, policy RetentionPolicy, log zerolog.Logger) *RetentionWorker {
	if policy.BatchSize <= 0 {
		policy.BatchSize = 500
	}
	if policy.MaxBatches <= 0 {
		policy.MaxBatches = 20
	}
	return &RetentionWorker{repo: repo, policy: policy, log: log, now: time.Now}
}

// Run cleans up once per interval until ctx is cancelled.
func (w *RetentionWorker) Run(ctx context.Context) {
	if !w.policy.Enabled {
		w.log.Info().Msg("cart_retention_disabled")
		return
	}
	ticker := time.NewTicker(w.policy.Interval)
	defer ticker.Stop()
	for {
		carts, ops, err := w.RunOnce(ctx)
		if err != nil {
			w.log.Error().Err(err).Msg("cart_retention_failed")
		} else if carts > 0 || ops > 0 {
			w.log.Info().Int64("carts_deleted", carts).Int64("operations_deleted", ops).Msg("cart_retention_completed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce purges operations past their TTL first, then idle carts, in
// bounded batches.
func (w *RetentionWorker) RunOnce(ctx context.Context) (cartsDeleted, operationsDeleted int64, err error) {
	now := w.now()
	for i := 0; i < w.policy.MaxBatches; i++ {
		n, err := w.repo.PurgeOperations(ctx, now.Add(-w.policy.OperationTTL), w.policy.BatchSize)
		if err != nil {
			return cartsDeleted, operationsDeleted, err
		}
		operationsDeleted += n
		if n < int64(w.policy.BatchSize) {
			break
		}
	}
	for i := 0; i < w.policy.MaxBatches; i++ {
		n, err := w.repo.PurgeIdleCarts(ctx, now.Add(-w.policy.CartIdle), w.policy.BatchSize)
		if err != nil {
			return cartsDeleted, operationsDeleted, err
		}
		cartsDeleted += n
		if n < int64(w.policy.BatchSize) {
			break
		}
	}
	return cartsDeleted, operationsDeleted, nil
}
