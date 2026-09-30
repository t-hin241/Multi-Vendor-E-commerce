package usecase

import (
	"context"
	"errors"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

const (
	// inlineConsumeTimeout bounds the consume attempt made during the
	// buyer's own checkout request; the worker takes over after that.
	inlineConsumeTimeout = 3 * time.Second
	// staleHeldAfter is how long a held task may wait for its checkout to
	// report success or failure before the worker resolves it itself.
	staleHeldAfter = 2 * time.Minute
	// abandonHeldAfter: a held task whose order never got a reservation is
	// cancelled after this long, leaving the cart untouched.
	abandonHeldAfter = 30 * time.Minute
)

// settleOpenCartConsumptions keeps a buyer from checking out the same cart
// twice while an earlier order's cart lines have not been consumed yet
// (for example when Cart was briefly down). A due task is retried right
// away; if anything is still open the buyer is told to look at their
// orders instead of silently buying again.
func (uc *OrderUseCase) settleOpenCartConsumptions(ctx context.Context, buyerID string) error {
	open, err := uc.CartConsumption.ListOpenByBuyer(ctx, buyerID)
	if err != nil {
		return apperror.Internal(err)
	}
	for _, task := range open {
		if task.Status == domain.CartConsumptionPending && uc.consumeCart(ctx, task) {
			continue
		}
		return apperror.Conflict("Your previous order is still being finalized. Please check My Orders before placing another order.")
	}
	return nil
}

// consumeCart tries one consume of a pending task and records the outcome.
// It never fails the caller; it reports whether the task is now consumed.
func (uc *OrderUseCase) consumeCart(ctx context.Context, task *domain.CartConsumption) bool {
	callCtx, cancel := context.WithTimeout(ctx, inlineConsumeTimeout)
	defer cancel()

	lines := make([]adapter.CartConsumeLine, 0, len(task.Lines))
	for _, l := range task.Lines {
		lines = append(lines, adapter.CartConsumeLine{LineID: l.LineID, Quantity: l.Quantity})
	}
	err := uc.Cart.Consume(callCtx, task.BuyerID, task.OperationID, lines)
	if err == nil {
		if markErr := uc.CartConsumption.MarkConsumed(ctx, task.OrderID); markErr != nil {
			// Cart already has the receipt; the next retry replays it.
			uc.Log.Error().Err(markErr).Str("order_id", task.OrderID).Msg("order_cart_consume_mark_failed")
			return false
		}
		return true
	}

	permanent := isPermanentCartError(err)
	if recordErr := uc.CartConsumption.RecordFailure(ctx, task.OrderID, err.Error(), permanent); recordErr != nil {
		uc.Log.Error().Err(recordErr).Str("order_id", task.OrderID).Msg("order_cart_consume_record_failed")
	}
	event := uc.Log.Warn()
	msg := "order_cart_consume_retry"
	if permanent || task.Attempts+1 >= domain.MaxCartConsumeAttempts {
		event, msg = uc.Log.Error(), "order_cart_consume_parked"
	}
	event.Err(err).Str("order_id", task.OrderID).Str("operation_id", task.OperationID).Int("attempts", task.Attempts+1).Msg(msg)
	return false
}

// isPermanentCartError: Cart answered and refused (4xx other than 408/429)
// — retrying the same request cannot succeed.
func isPermanentCartError(err error) bool {
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code == apperror.CodeInternal {
		return false
	}
	return appErr.Status >= 400 && appErr.Status < 500 && appErr.Status != 408 && appErr.Status != 429
}

// ProcessCartConsumptions is one worker tick: resolve checkouts that never
// reported back, then retry due consume tasks.
func (uc *OrderUseCase) ProcessCartConsumptions(ctx context.Context, batch int) (processed int, err error) {
	stale, err := uc.CartConsumption.ListStaleHeld(ctx, time.Now().Add(-staleHeldAfter), batch)
	if err != nil {
		return 0, err
	}
	for _, task := range stale {
		uc.resolveHeld(ctx, task)
	}

	due, err := uc.CartConsumption.ClaimDue(ctx, batch)
	if err != nil {
		return 0, err
	}
	for _, task := range due {
		uc.consumeCart(ctx, task)
	}
	return len(stale) + len(due), nil
}

// resolveHeld decides a held task from what actually happened to its order:
// a live reservation means the order stands (consume), a cancelled order or
// released reservation means it does not (leave the cart alone).
func (uc *OrderUseCase) resolveHeld(ctx context.Context, task *domain.CartConsumption) {
	logger := uc.Log.With().Str("order_id", task.OrderID).Logger()
	order, err := uc.Orders.FindByID(ctx, task.OrderID)
	if err != nil {
		if !errors.Is(err, repository.ErrOrderNotFound) {
			logger.Error().Err(err).Msg("order_cart_consume_resolve_failed")
		}
		return
	}
	if order.Status == domain.StatusCancelled {
		if err := uc.CartConsumption.Cancel(ctx, task.OrderID); err != nil {
			logger.Error().Err(err).Msg("order_cart_consume_resolve_failed")
		}
		return
	}

	receipt, err := uc.Inventory.Operation(ctx, task.OrderID)
	var appErr *apperror.Error
	switch {
	case err == nil && (receipt.Status == "held" || receipt.Status == "committed"):
		if err := uc.CartConsumption.Activate(ctx, task.OrderID); err != nil {
			logger.Error().Err(err).Msg("order_cart_consume_resolve_failed")
		}
	case err == nil && (receipt.Status == "released" || receipt.Status == "expired"),
		errors.As(err, &appErr) && appErr.Code == apperror.CodeNotFound && time.Since(task.CreatedAt) > abandonHeldAfter:
		if err := uc.CartConsumption.Cancel(ctx, task.OrderID); err != nil {
			logger.Error().Err(err).Msg("order_cart_consume_resolve_failed")
		}
	case err != nil && !(errors.As(err, &appErr) && appErr.Code == apperror.CodeNotFound):
		logger.Warn().Err(err).Msg("order_cart_consume_resolve_deferred")
	}
}
