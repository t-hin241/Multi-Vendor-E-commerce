// Package usecase orchestrates Payment's workflows: payment intents as
// persisted operations, verified provider receipts applied exactly once,
// reconciliation with the provider, refunds and vendor settlement.
package usecase

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/provider"
	"shopee/backend/services/payment/internal/repository"
)

const (
	providerCallTimeout = 20 * time.Second
	// creatingGrace: an intent still 'creating' after this is treated as a
	// provider call whose outcome is unknown.
	creatingGrace = 30 * time.Second
	// creatingGiveUp: after this, an intent whose provider link cannot be
	// verified is closed; a late capture on it is still recorded.
	creatingGiveUp = 15 * time.Minute
)

// ProviderUnavailable is returned when the provider could not create a
// link; the buyer may retry.
func ProviderUnavailable() *apperror.Error {
	return &apperror.Error{Code: "payment_provider_unavailable", Status: 503, Message: "The payment provider is unavailable. Please try again in a moment."}
}

// PaymentInProgress: another attempt for the order is being prepared.
func PaymentInProgress() *apperror.Error {
	return &apperror.Error{Code: "payment_in_progress", Status: 409, Message: "A payment for this order is being prepared. Please try again in a few seconds."}
}

type PaymentDeps struct {
	Tx        Transactor
	Intents   PaymentIntentRepositoryPort
	Receipts  ReceiptRepositoryPort
	Orders    OrderGateway
	Provider  provider.Provider
	Verifier  provider.Verifier
	Simulator Simulator
	// ProviderName is stored on every intent and receipt.
	ProviderName string
	ReturnURL    string
	CancelURL    string
	// SyncNow asks the Order outbox to deliver an intent's outcome right
	// away; the outbox worker retries it anyway.
	SyncNow func(ctx context.Context, intentID string)
	Log     zerolog.Logger
	Now     func() time.Time
}

type PaymentUseCase struct{ PaymentDeps }

func NewPaymentUseCase(d PaymentDeps) *PaymentUseCase {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &PaymentUseCase{d}
}

// CreateIntent starts payment for an order. The amount comes from Order,
// never from the client. The intent is persisted with a stable provider
// reference before the provider is called; an order has at most one open
// intent, and an earlier attempt whose outcome is unknown is resolved with
// the provider before a new link is made.
func (uc *PaymentUseCase) CreateIntent(ctx context.Context, buyerID, orderID string) (*domain.PaymentIntent, error) {
	order, err := uc.Orders.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order.BuyerID != buyerID {
		return nil, apperror.Forbidden("You do not have access to this order")
	}
	if order.Status != "pending_payment" {
		return nil, apperror.Conflict("This order is not awaiting payment")
	}
	now := uc.Now()
	if order.InventoryStatus != "held" || order.ReservationExpiresAt == nil || !order.ReservationExpiresAt.After(now) {
		return nil, apperror.Conflict("Order has no active payment reservation window")
	}
	if err := domain.ValidateAmount(order.TotalAmount); err != nil {
		return nil, err
	}

	existing, err := uc.Intents.FindOpenByOrderID(ctx, orderID)
	switch {
	case errors.Is(err, repository.ErrPaymentIntentNotFound):
	case err != nil:
		return nil, apperror.Internal(err)
	case existing.BuyerID != buyerID:
		return nil, apperror.Forbidden("You do not have access to this order")
	case existing.Status == domain.StatusPending && existing.Payable(now) && existing.Amount == order.TotalAmount && existing.Currency == order.Currency:
		return existing, nil
	case existing.Status == domain.StatusCreating && now.Sub(existing.UpdatedAt) < creatingGrace:
		return nil, PaymentInProgress()
	default:
		// A stale link, or an attempt whose outcome is unknown: settle it
		// with the provider first so the buyer can never pay twice.
		resolved, err := uc.resolveOpen(ctx, existing, true)
		if err != nil {
			return nil, err
		}
		if resolved != nil {
			return resolved, nil
		}
	}

	reference, err := uc.newReference(ctx)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	intent := &domain.PaymentIntent{OrderID: orderID, BuyerID: buyerID, Amount: order.TotalAmount, Currency: order.Currency,
		Provider: uc.ProviderName, ProviderReference: reference, ExpiresAt: order.ReservationExpiresAt}
	if err := uc.Intents.CreateOperation(ctx, intent); err != nil {
		if errors.Is(err, repository.ErrOpenIntentExists) {
			return nil, PaymentInProgress()
		}
		return nil, apperror.Internal(err)
	}
	return uc.link(ctx, intent)
}

// newReference is the provider's idempotency key for one attempt.
func (uc *PaymentUseCase) newReference(ctx context.Context) (string, error) {
	if uc.ProviderName == "payos" {
		code, err := uc.Intents.NextOrderCode(ctx)
		return strconv.FormatInt(code, 10), err
	}
	return uuid.NewString(), nil
}

// link calls the provider for a 'creating' intent and records the result.
func (uc *PaymentUseCase) link(ctx context.Context, intent *domain.PaymentIntent) (*domain.PaymentIntent, error) {
	callCtx, cancel := context.WithTimeout(ctx, providerCallTimeout)
	defer cancel()
	result, err := uc.Provider.CreateIntent(callCtx, provider.CreateIntentInput{
		Reference: intent.ProviderReference, ExpiresAt: intent.ExpiresAt, OrderID: intent.OrderID, Amount: intent.Amount, Currency: intent.Currency,
		ReturnURL: paymentReturnURL(uc.ReturnURL, intent.OrderID), CancelURL: paymentReturnURL(uc.CancelURL, intent.OrderID),
	})
	log := uc.Log.With().Str("payment_intent_id", intent.ID).Str("order_id", intent.OrderID).Logger()
	if err != nil {
		bg := context.WithoutCancel(ctx)
		if e := uc.Intents.RecordCreateFailure(bg, intent.ID, err.Error()); e != nil {
			log.Error().Err(e).Msg("payment_create_failure_record_failed")
		}
		if errors.Is(err, provider.ErrRejected) {
			// The provider refused: no link exists, close the attempt.
			if e := uc.Intents.Close(bg, intent.ID, "create_rejected"); e != nil && !errors.Is(e, repository.ErrStaleState) {
				log.Error().Err(e).Msg("payment_intent_close_failed")
			}
			log.Warn().Err(err).Msg("payment_link_rejected")
		} else {
			// Unknown outcome: stays 'creating'; the next attempt or the
			// reconciler asks the provider before anything else.
			log.Warn().Err(err).Msg("payment_link_outcome_unknown")
		}
		return nil, ProviderUnavailable()
	}
	if intent.ExpiresAt != nil && (result.ExpiresAt == nil || result.ExpiresAt.After(*intent.ExpiresAt)) {
		result.ExpiresAt = intent.ExpiresAt
	}
	if err := uc.Intents.MarkLinked(ctx, intent.ID, result); err != nil && !errors.Is(err, repository.ErrStaleState) {
		return nil, apperror.Internal(err)
	}
	stored, err := uc.Intents.FindByID(ctx, intent.ID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if !stored.Payable(uc.Now()) {
		// A webhook or the reconciler moved it meanwhile.
		return nil, apperror.Conflict("This payment is no longer open")
	}
	log.Info().Msg("payment_link_created")
	return stored, nil
}

// resolveOpen settles an open intent with the provider. It returns a
// payable intent when the earlier attempt can be reused, nil when the
// caller may create a new one, or an error when the order must not be
// charged again yet (it was paid, or the provider cannot be reached).
func (uc *PaymentUseCase) resolveOpen(ctx context.Context, intent *domain.PaymentIntent, forBuyer bool) (*domain.PaymentIntent, error) {
	log := uc.Log.With().Str("payment_intent_id", intent.ID).Str("order_id", intent.OrderID).Logger()
	_ = uc.Intents.Touch(ctx, intent.ID)
	if intent.ProviderReference == "" {
		// Created by the previous version: its provider reference was never
		// stored, so it cannot be queried. Close it only once expired; a
		// late capture is still recorded.
		if intent.ExpiresAt != nil && intent.ExpiresAt.After(uc.Now()) {
			return nil, apperror.Conflict("An earlier payment link for this order is still open")
		}
		return nil, uc.close(ctx, intent, "legacy_expired_unverified")
	}
	callCtx, cancel := context.WithTimeout(ctx, providerCallTimeout)
	defer cancel()
	info, err := uc.Provider.Query(callCtx, intent.ProviderReference)
	switch {
	case errors.Is(err, provider.ErrNotFound):
		if intent.Status == domain.StatusCreating && forBuyer {
			// No link was made: retry the same reference.
			return uc.link(ctx, intent)
		}
		return nil, uc.close(ctx, intent, "not_found_at_provider")
	case err != nil:
		if intent.Status == domain.StatusCreating && uc.Now().Sub(intent.UpdatedAt) > creatingGiveUp {
			log.Warn().Err(err).Msg("payment_link_unverified_closed")
			_ = uc.Provider.Cancel(callCtx, intent.ProviderReference, "unverified")
			return nil, uc.close(ctx, intent, "unverified_timeout")
		}
		log.Warn().Err(err).Msg("payment_provider_query_failed")
		return nil, ProviderUnavailable()
	}
	switch info.Status {
	case provider.LinkPaid:
		if err := uc.recordFinding(ctx, intent, info); err != nil {
			return nil, err
		}
		return nil, apperror.Conflict("This order has already been paid; its status will update shortly")
	case provider.LinkClosed:
		return nil, uc.close(ctx, intent, "provider_closed")
	default:
		// Open at the provider but not usable here (no confirmed link, or
		// past the reservation): cancel it before anything else.
		if err := uc.Provider.Cancel(callCtx, intent.ProviderReference, "superseded"); err != nil && !errors.Is(err, provider.ErrNotFound) {
			log.Warn().Err(err).Msg("payment_link_cancel_failed")
			return nil, ProviderUnavailable()
		}
		return nil, uc.close(ctx, intent, "superseded_cancelled")
	}
}

func (uc *PaymentUseCase) close(ctx context.Context, intent *domain.PaymentIntent, reason string) error {
	if err := uc.Intents.Close(ctx, intent.ID, reason); err != nil && !errors.Is(err, repository.ErrStaleState) {
		return apperror.Internal(err)
	}
	uc.Log.Info().Str("payment_intent_id", intent.ID).Str("order_id", intent.OrderID).Str("reason", reason).Msg("payment_intent_closed")
	return nil
}

// recordFinding turns a provider query that shows the link paid into a
// receipt, keyed on the provider's transaction reference so the webhook for
// the same payment deduplicates against it.
func (uc *PaymentUseCase) recordFinding(ctx context.Context, intent *domain.PaymentIntent, info provider.LinkInfo) error {
	eventID := "txn:" + info.TransactionReference
	if info.TransactionReference == "" {
		eventID = "query:" + intent.ProviderReference + ":paid"
	}
	currency := info.Currency
	if currency == "" {
		currency = intent.Currency
	}
	receipt, _, err := uc.Receipts.Record(ctx, &domain.Receipt{Provider: uc.ProviderName, ProviderEventID: eventID, ProviderIntentID: info.ProviderIntentID,
		ProviderReference: intent.ProviderReference, EventType: domain.EventSucceeded, Amount: info.AmountPaid, Currency: currency})
	if err != nil {
		return apperror.Internal(err)
	}
	uc.Log.Warn().Str("payment_intent_id", intent.ID).Str("order_id", intent.OrderID).Msg("payment_capture_found_by_reconciliation")
	return uc.applyReceipt(ctx, receipt.ID)
}

// paymentReturnURL adds a non-authoritative navigation hint. The page reached
// after redirect must still read the Order/Payment API; it never trusts
// provider query parameters.
func paymentReturnURL(base, orderID string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	q.Set("order_id", orderID)
	u.RawQuery = q.Encode()
	return u.String()
}

func (uc *PaymentUseCase) GetOwned(ctx context.Context, buyerID, intentID string) (*domain.PaymentIntent, error) {
	return uc.findOwned(ctx, buyerID, intentID)
}

// Simulate stands in for a real provider's hosted checkout page. It builds
// the same signed event a webhook would carry and feeds it through
// ProcessWebhook. It only works with the mock provider.
func (uc *PaymentUseCase) Simulate(ctx context.Context, buyerID, intentID string, succeeded bool, failureReason string) (*domain.PaymentIntent, error) {
	if uc.Simulator == nil {
		return nil, apperror.Validation("Simulating a payment outcome is only available with the mock payment provider")
	}
	intent, err := uc.findOwned(ctx, buyerID, intentID)
	if err != nil {
		return nil, err
	}
	if intent.Status != domain.StatusPending {
		return nil, apperror.Conflict("This payment has already been processed")
	}
	payload, signature, err := uc.Simulator.BuildSignedEvent(intent.ProviderIntentID, intent.Amount, intent.Currency, succeeded, failureReason)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if err := uc.ProcessWebhook(ctx, payload, signature); err != nil {
		return nil, err
	}
	return uc.Intents.FindByID(ctx, intent.ID)
}

// ProcessWebhook verifies a delivery, records it as a receipt, then applies
// it. An error tells the provider to retry: the receipt is kept either way.
func (uc *PaymentUseCase) ProcessWebhook(ctx context.Context, payload []byte, signatureHeader string) error {
	event, err := uc.Verifier.Verify(payload, signatureHeader)
	if err != nil {
		uc.Log.Warn().Msg("payment_webhook_invalid_signature")
		return apperror.Unauthorized("Invalid webhook signature")
	}
	receipt, created, err := uc.Receipts.Record(ctx, &domain.Receipt{
		Provider: uc.ProviderName, ProviderEventID: event.ProviderEventID, ProviderIntentID: event.ProviderIntentID,
		ProviderReference: event.ProviderReference, EventType: domain.EventType(event.Type), Amount: event.Amount,
		Currency: event.Currency, FailureReason: event.FailureReason,
	})
	if err != nil {
		return apperror.Internal(err)
	}
	if receipt.Done() {
		uc.Log.Info().Str("receipt_id", receipt.ID).Str("provider_event_id", event.ProviderEventID).Msg("payment_webhook_duplicate")
		return nil
	}
	if !created {
		uc.Log.Info().Str("receipt_id", receipt.ID).Msg("payment_webhook_redelivered_unfinished")
	}
	return uc.applyReceipt(ctx, receipt.ID)
}

// applyReceipt applies one receipt in a single transaction: the intent's
// transition, the Order outbox row (by trigger) and the receipt's final
// status commit together. Nothing is marked processed before that.
func (uc *PaymentUseCase) applyReceipt(ctx context.Context, receiptID string) error {
	var syncIntent string
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		r, err := uc.Receipts.Lock(ctx, receiptID)
		if errors.Is(err, repository.ErrReceiptNotFound) {
			return nil // another worker holds it
		}
		if err != nil || r.Done() {
			return err
		}
		log := uc.Log.With().Str("receipt_id", r.ID).Str("provider_event_id", r.ProviderEventID).Logger()
		intent, err := uc.Intents.LockForEvent(ctx, r.Provider, r.ProviderIntentID, r.ProviderReference)
		if errors.Is(err, repository.ErrPaymentIntentNotFound) {
			log.Warn().Str("provider_intent_id", r.ProviderIntentID).Msg("payment_receipt_parked")
			return uc.Receipts.Park(ctx, r.ID)
		}
		if err != nil {
			return err
		}
		decision := domain.DecideReceipt(intent, r)
		if decision.NewStatus != "" {
			var reason *string
			if decision.FailureReason != "" {
				reason = &decision.FailureReason
			}
			if err := uc.Intents.Transition(ctx, intent.ID, intent.Status, decision.NewStatus, reason); err != nil {
				return err
			}
			syncIntent = intent.ID
		}
		if err := uc.Receipts.Finish(ctx, r.ID, &intent.ID, decision.ReceiptStatus, decision.Outcome); err != nil {
			return err
		}
		if err := uc.Receipts.RecordLegacyEvent(ctx, r.ProviderEventID, intent.ID, r.EventType); err != nil {
			return err
		}
		event := log.Info()
		if decision.Outcome == domain.OutcomeAmountMismatch {
			event = log.Error()
		}
		event.Str("payment_intent_id", intent.ID).Str("order_id", intent.OrderID).Str("outcome", decision.Outcome).
			Str("status", string(decision.NewStatus)).Msg("payment_receipt_applied")
		return nil
	})
	if err != nil {
		if e := uc.Receipts.MarkRetryable(context.WithoutCancel(ctx), receiptID, err.Error()); e != nil {
			uc.Log.Error().Err(e).Str("receipt_id", receiptID).Msg("payment_receipt_retry_record_failed")
		}
		uc.Log.Error().Err(err).Str("receipt_id", receiptID).Msg("payment_receipt_apply_failed")
		return apperror.Internal(err)
	}
	if syncIntent != "" && uc.SyncNow != nil {
		uc.SyncNow(ctx, syncIntent)
	}
	return nil
}

// Reconcile is one worker round: re-apply receipts that did not finish and
// check intents whose provider link is stale.
func (uc *PaymentUseCase) Reconcile(ctx context.Context, batch int) error {
	receipts, err := uc.Receipts.ListDue(ctx, batch)
	if err != nil {
		return err
	}
	for _, r := range receipts {
		if err := uc.applyReceipt(ctx, r.ID); err != nil && ctx.Err() == nil {
			uc.Log.Warn().Err(err).Str("receipt_id", r.ID).Msg("payment_receipt_retry_failed")
		}
	}
	intents, err := uc.Intents.ListForReconciliation(ctx, 2*time.Minute, 5*time.Minute, batch)
	if err != nil {
		return err
	}
	for _, intent := range intents {
		if _, err := uc.resolveOpen(ctx, intent, false); err != nil && ctx.Err() == nil {
			uc.Log.Info().Err(err).Str("payment_intent_id", intent.ID).Msg("payment_intent_reconcile_deferred")
		}
	}
	return nil
}

func (uc *PaymentUseCase) findOwned(ctx context.Context, buyerID, intentID string) (*domain.PaymentIntent, error) {
	intent, err := uc.Intents.FindByID(ctx, intentID)
	if err != nil {
		if errors.Is(err, repository.ErrPaymentIntentNotFound) {
			return nil, apperror.NotFound("Payment not found")
		}
		return nil, apperror.Internal(err)
	}
	if intent.BuyerID != buyerID {
		return nil, apperror.Forbidden("You do not have access to this payment")
	}
	return intent, nil
}
