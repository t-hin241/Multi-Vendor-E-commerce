package usecase

import (
	"context"
	"errors"
	"shopee/backend/pkg/apperror"
	"shopee/backend/services/inventory/internal/domain"
	"shopee/backend/services/inventory/internal/repository"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

type OperationsRepository interface {
	SyncStock(context.Context, func(context.Context, []string) error) error
	Stats(context.Context) (map[string]int64, error)
	Issues(context.Context, int, int) ([]domain.OperationIssue, error)
	Candidates(context.Context) ([]string, error)
	Observe(context.Context, string, string, string) error
	Repair(context.Context, string, string, string, string) error
	Dispatch(context.Context, func(context.Context, domain.OutboxEvent) error) error
}
type ReservationOperations interface {
	Operation(context.Context, string) (*domain.Operation, error)
	ExpireOne(context.Context) error
}
type OrderOperations interface {
	Status(context.Context, string) (string, error)
	Publish(context.Context, domain.OutboxEvent) error
}
type Maintenance struct {
	InvalidateStock func(context.Context, []string) error
	Repository      OperationsRepository
	Reservations    ReservationOperations
	Orders          OrderOperations
	Identity        Identity
	Log             zerolog.Logger
	ExpiryEnabled   bool
}

func (m Maintenance) Stats(ctx context.Context, actor string) (map[string]int64, error) {
	if err := m.Identity.RequireRole(ctx, actor, "admin"); err != nil {
		return nil, err
	}
	v, err := m.Repository.Stats(ctx)
	if err == nil {
		v["expiry_enabled"] = 0
		if m.ExpiryEnabled {
			v["expiry_enabled"] = 1
		}
	}
	return v, inventoryError(err)
}
func (m Maintenance) Issues(ctx context.Context, actor string, limit, offset int) ([]domain.OperationIssue, error) {
	if err := m.Identity.RequireRole(ctx, actor, "admin"); err != nil {
		return nil, err
	}
	v, err := m.Repository.Issues(ctx, limit, offset)
	return v, inventoryError(err)
}
func (m Maintenance) Repair(ctx context.Context, actor, id, action, reason string) error {
	if err := m.Identity.RequireRole(ctx, actor, "admin"); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return apperror.Validation("A reason of at most 500 bytes is required")
	}
	if action == "release_cancelled" || action == "adopt_legacy" {
		status, err := m.Orders.Status(ctx, id)
		if err != nil {
			return inventoryError(err)
		}
		if action == "release_cancelled" && status != "cancelled" || action == "adopt_legacy" && status != "pending_payment" {
			return apperror.Conflict("Order status does not allow this repair")
		}
	}
	return inventoryError(m.Repository.Repair(ctx, id, actor, action, reason))
}
func (m Maintenance) Reconcile(ctx context.Context) error {
	ids, err := m.Repository.Candidates(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		o, err := m.Reservations.Operation(ctx, id)
		if err != nil {
			return err
		}
		status, lookupErr := m.Orders.Status(ctx, id)
		issue := ""
		switch {
		case lookupErr != nil:
			issue = "order_unavailable"
		case status == "missing":
			issue = "order_missing"
		case o.Status == "held" && status != "pending_payment":
			issue = "held_order_" + status
		case o.Status == "committed" && (status == "pending_payment" || status == "cancelled"):
			issue = "committed_order_" + status
		case (o.Status == "expired" || o.Status == "released") && status != "cancelled":
			issue = "terminal_stock_order_" + status
		}
		if err := m.Repository.Observe(ctx, id, o.Status, issue); err != nil {
			return err
		}
	}
	return nil
}

// alertKeys are the counters that need an operator when non-zero: holds
// past their deadline, work that ran out of retries, and drift between
// Inventory, Order and the reserved balance.
var alertKeys = []string{"overdue", "expiry_parked", "events_parked", "cache_parked", "order_mismatches", "reserved_mismatches"}

// AlertCounters returns the non-zero alert counters from a stats snapshot.
func AlertCounters(stats map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for _, k := range alertKeys {
		if stats[k] > 0 {
			out[k] = stats[k]
		}
	}
	return out
}

// reportAlerts logs one structured warning per minute while any alert
// counter is non-zero, so log-based alerting can page on it.
func (m Maintenance) reportAlerts(ctx context.Context) {
	stats, err := m.Repository.Stats(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.Log.Error().Err(err).Msg("inventory_stats_failed")
		}
		return
	}
	alerts := AlertCounters(stats)
	if len(alerts) == 0 {
		return
	}
	event := m.Log.Warn().Bool("expiry_enabled", m.ExpiryEnabled).Int64("held", stats["held"]).Int64("legacy_held", stats["legacy_held"])
	for k, v := range alerts {
		event = event.Int64(k, v)
	}
	event.Msg("inventory_operations_alert")
}

func (m Maintenance) Run(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	iteration := 0
	for {
		if m.ExpiryEnabled {
			for i := 0; i < 50 && ctx.Err() == nil; i++ {
				err := m.Reservations.ExpireOne(ctx)
				if errors.Is(err, repository.ErrNoDueReservation) {
					break
				}
				if err != nil {
					m.Log.Error().Err(err).Msg("inventory_expiry_failed")
					break
				}
			}
		}
		for i := 0; i < 50 && ctx.Err() == nil; i++ {
			err := m.Repository.Dispatch(ctx, m.Orders.Publish)
			if errors.Is(err, repository.ErrNoInventoryEvent) {
				break
			}
			if err != nil {
				m.Log.Error().Err(err).Msg("inventory_outbox_failed")
				break
			}
		}
		if m.InvalidateStock != nil {
			if err := m.Repository.SyncStock(ctx, m.InvalidateStock); err != nil {
				m.Log.Error().Err(err).Msg("inventory_cache_sync_failed")
			}
		}
		if iteration%12 == 0 {
			batch, cancel := context.WithTimeout(ctx, 30*time.Second)
			if err := m.Reconcile(batch); err != nil {
				m.Log.Error().Err(err).Msg("inventory_reconciliation_failed")
			}
			m.reportAlerts(batch)
			cancel()
		}
		iteration++
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
